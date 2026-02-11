package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/toricodesthings/file-processing-service/internal/config"
	"github.com/toricodesthings/file-processing-service/internal/hybrid"
	"github.com/toricodesthings/file-processing-service/internal/image"
	"github.com/toricodesthings/file-processing-service/internal/types"
	"golang.org/x/sync/semaphore"
	"golang.org/x/time/rate"
)

var (
	cfg config.Config

	requestSem *semaphore.Weighted
	ocrSem     *semaphore.Weighted

	// Per-IP rate limiters
	limiters = &sync.Map{}

	metrics = &serverMetrics{}
)

type serverMetrics struct {
	mu            sync.RWMutex
	totalRequests int64
	activeReqs    int64
}

func (m *serverMetrics) incActive() {
	m.mu.Lock()
	m.activeReqs++
	m.totalRequests++
	m.mu.Unlock()
}
func (m *serverMetrics) decActive() {
	m.mu.Lock()
	m.activeReqs--
	m.mu.Unlock()
}
func (m *serverMetrics) get() (total, active int64) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.totalRequests, m.activeReqs
}

func main() {
	cfg = config.Load()
	if err := cfg.Validate(); err != nil {
		panic(err)
	}

	requestSem = semaphore.NewWeighted(cfg.MaxConcurrentRequests)
	ocrSem = semaphore.NewWeighted(cfg.MaxOCRConcurrent)

	processor := hybrid.New(cfg)

	mux := http.NewServeMux()

	mux.HandleFunc("/health", handleHealth)
	mux.HandleFunc("/metrics", withInternalAuth(handleMetrics))

	mux.HandleFunc("/pdf/extract",
		withInternalAuth(
			withRateLimit(
				withMethod("POST",
					withConcurrencyLimit(func(w http.ResponseWriter, r *http.Request) {
						handleExtract(w, r, processor)
					})))))

	mux.HandleFunc("/pdf/preview",
		withInternalAuth(
			withRateLimit(
				withMethod("POST",
					withConcurrencyLimit(func(w http.ResponseWriter, r *http.Request) {
						handlePreview(w, r, processor)
					})))))

	mux.HandleFunc("/image/extract",
		withInternalAuth(
			withRateLimit(
				withMethod("POST",
					withConcurrencyLimit(func(w http.ResponseWriter, r *http.Request) {
						handleImageExtract(w, r)
					})))))

	maxHeaderBytes := 1 << 20
	if cfg.MaxHeaderBytes > 0 {
		maxHeaderBytes = cfg.MaxHeaderBytes
	}

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           withLogging(withRecovery(mux)),
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		ReadTimeout:       cfg.ReadTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       cfg.IdleTimeout,
		MaxHeaderBytes:    maxHeaderBytes,
	}

	if strings.TrimSpace(cfg.MistralAPIKey) == "" {
		fmt.Fprintln(os.Stderr, "warning: MISTRAL_API_KEY not set (OCR will fail)")
	}

	go cleanupRateLimiters()

	fmt.Printf("fileproc listening on %s (max concurrent: %d, OCR: %d)\n",
		srv.Addr, cfg.MaxConcurrentRequests, cfg.MaxOCRConcurrent)

	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		panic(err)
	}
}

func cleanupRateLimiters() {
	interval := cfg.CleanupInterval
	if interval <= 0 {
		interval = 5 * time.Minute
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for range ticker.C {
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		total, active := metrics.get()
		fmt.Printf("[stats] active=%d total=%d goroutines=%d mem=%dMB\n",
			active, total, runtime.NumGoroutine(), m.Alloc/(1<<20))

		// simple clear (if you want smarter: store last-seen timestamps)
		limiters = &sync.Map{}
	}
}

// ---------- Handlers ----------

func handleHealth(w http.ResponseWriter, r *http.Request) {
	_, active := metrics.get()
	status := "healthy"
	code := http.StatusOK

	ratio := cfg.HealthDegradeRatio
	if ratio <= 0 || ratio > 1 {
		ratio = 0.9
	}

	if active >= int64(float64(cfg.MaxConcurrentRequests)*ratio) {
		status = "degraded"
		code = http.StatusServiceUnavailable
	}

	writeJSON(w, code, map[string]any{
		"status":  status,
		"active":  active,
		"version": "1.0.0",
	})
}

func handleMetrics(w http.ResponseWriter, r *http.Request) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	total, active := metrics.get()

	writeJSON(w, http.StatusOK, map[string]any{
		"activeRequests": active,
		"totalRequests":  total,
		"goroutines":     runtime.NumGoroutine(),
		"memAllocMB":     m.Alloc / (1 << 20),
		"memSysMB":       m.Sys / (1 << 20),
	})
}

func handleExtract(w http.ResponseWriter, r *http.Request, processor *hybrid.Processor) {
	req, err := parseJSON[types.ExtractRequest](r, cfg.MaxJSONBodyBytes)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", sanitizeError(err))
		return
	}

	if err := validateExtractRequest(req); err != nil {
		writeErr(w, http.StatusBadRequest, "validation_failed", sanitizeError(err))
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), cfg.ExtractTimeout)
	defer cancel()

	pdfPath, cleanup, err := downloadPDFToTemp(ctx, req.PresignedURL, cfg.MaxPDFBytes, cfg.DownloadTimeout)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "download_failed", sanitizeError(err))
		return
	}
	defer cleanup()

	opts := processor.ApplyDefaults(req.Options)

	// OCR capacity gating: acquire only if OCR might occur
	if opts.OCRTriggerRatio > 0 {
		if err := ocrSem.Acquire(ctx, 1); err != nil {
			writeErr(w, http.StatusServiceUnavailable, "ocr_capacity", "OCR at capacity")
			return
		}
		defer ocrSem.Release(1)
	}

	result, _ := processor.ProcessHybrid(ctx, req.PresignedURL, pdfPath, opts)
	writeJSON(w, http.StatusOK, result)
}

func handlePreview(w http.ResponseWriter, r *http.Request, processor *hybrid.Processor) {
	req, err := parseJSON[types.ExtractRequest](r, cfg.MaxJSONBodyBytes)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", sanitizeError(err))
		return
	}

	if err := validateExtractRequest(req); err != nil {
		writeErr(w, http.StatusBadRequest, "validation_failed", sanitizeError(err))
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), cfg.PreviewTimeout)
	defer cancel()

	pdfPath, cleanup, err := downloadPDFToTemp(ctx, req.PresignedURL, cfg.MaxPDFBytes, cfg.DownloadTimeout)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "download_failed", sanitizeError(err))
		return
	}
	defer cleanup()

	opts := processor.ApplyDefaults(req.Options)
	prev := processor.ProcessPreview(ctx, pdfPath, opts)

	writeJSON(w, http.StatusOK, prev)
}

func handleImageExtract(w http.ResponseWriter, r *http.Request) {
	req, err := parseJSON[types.ImageExtractRequest](r, cfg.MaxJSONBodyBytes)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "bad_request", sanitizeError(err))
		return
	}

	if err := validateImageRequest(req); err != nil {
		writeErr(w, http.StatusBadRequest, "validation_failed", sanitizeError(err))
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), cfg.ImageExtractTimeout)
	defer cancel()

	// OCR capacity gating
	if err := ocrSem.Acquire(ctx, 1); err != nil {
		writeErr(w, http.StatusServiceUnavailable, "ocr_capacity", "OCR at capacity")
		return
	}
	defer ocrSem.Release(1)

	result, _ := image.ProcessImageOCR(ctx, req.ImageURL, cfg.DefaultOCRModel)
	writeJSON(w, http.StatusOK, result)
}

// ---------- Middleware ----------

func withMethod(method string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != method {
			w.Header().Set("Allow", method)
			writeErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "Method must be "+method)
			return
		}
		next(w, r)
	}
}

func withInternalAuth(next http.HandlerFunc) http.HandlerFunc {
	shared := cfg.InternalSharedSecret
	return func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get("X-Internal-Auth")
		if subtle.ConstantTimeCompare([]byte(got), []byte(shared)) != 1 {
			writeErr(w, http.StatusUnauthorized, "unauthorized", "Invalid authentication")
			return
		}
		next(w, r)
	}
}

func withConcurrencyLimit(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := requestSem.Acquire(r.Context(), 1); err != nil {
			writeErr(w, http.StatusServiceUnavailable, "capacity", "Service at capacity")
			return
		}
		defer requestSem.Release(1)

		metrics.incActive()
		defer metrics.decActive()

		next(w, r)
	}
}

func withRateLimit(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ip := getClientIP(r)
		limiter := getRateLimiter(ip)

		if !limiter.Allow() {
			w.Header().Set("Retry-After", "60")
			writeErr(w, http.StatusTooManyRequests, "rate_limit", "Rate limit exceeded")
			return
		}
		next(w, r)
	}
}

func withRecovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				fmt.Fprintf(os.Stderr, "panic: %v\n", err)
				writeErr(w, http.StatusInternalServerError, "internal_error", "Internal server error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := &wrapWriter{ResponseWriter: w, status: 200}
		next.ServeHTTP(ww, r)

		fmt.Printf("%s %s -> %d (%s)\n",
			r.Method, sanitizeLogString(r.URL.Path), ww.status, time.Since(start))
	})
}

type wrapWriter struct {
	http.ResponseWriter
	status int
}

func (w *wrapWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// ---------- Helpers ----------

func getRateLimiter(ip string) *rate.Limiter {
	if v, ok := limiters.Load(ip); ok {
		return v.(*rate.Limiter)
	}

	every := cfg.RateLimitEvery
	if every <= 0 {
		every = 600 * time.Millisecond // ~100/min
	}
	burst := cfg.RateLimitBurst
	if burst <= 0 {
		burst = 20
	}

	limiter := rate.NewLimiter(rate.Every(every), burst)
	limiters.Store(ip, limiter)
	return limiter
}

func getClientIP(r *http.Request) string {
	if ip := r.Header.Get("X-Forwarded-For"); ip != "" {
		if idx := strings.Index(ip, ","); idx > 0 {
			return strings.TrimSpace(ip[:idx])
		}
		return strings.TrimSpace(ip)
	}
	if ip := r.Header.Get("X-Real-IP"); ip != "" {
		return strings.TrimSpace(ip)
	}

	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	return host
}

func validateExtractRequest(req types.ExtractRequest) error {
	url := strings.TrimSpace(req.PresignedURL)
	if url == "" {
		return fmt.Errorf("presignedUrl required")
	}
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return fmt.Errorf("presignedUrl must be http/https")
	}
	if len(url) > 2048 {
		return fmt.Errorf("presignedUrl too long")
	}
	return nil
}

func validateImageRequest(req types.ImageExtractRequest) error {
	url := strings.TrimSpace(req.ImageURL)
	if url == "" {
		return fmt.Errorf("imageUrl required")
	}
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return fmt.Errorf("imageUrl must be http/https")
	}
	maxLen := cfg.MaxImageURLLen
	if maxLen <= 0 {
		maxLen = 2048
	}
	if len(url) > maxLen {
		return fmt.Errorf("imageUrl too long")
	}
	lower := strings.ToLower(url)
	if strings.HasSuffix(lower, ".pdf") || strings.Contains(lower, ".pdf?") {
		return fmt.Errorf("PDF files should use the /extract endpoint, not /image/extract")
	}
	return nil
}

func sanitizeError(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	msg = strings.ReplaceAll(msg, os.TempDir(), "[tmp]")
	if len(msg) > 300 {
		msg = msg[:300] + "..."
	}
	return msg
}

func sanitizeLogString(s string) string {
	s = strings.ReplaceAll(s, "\n", "")
	s = strings.ReplaceAll(s, "\r", "")
	if len(s) > 200 {
		s = s[:200] + "..."
	}
	return s
}

func parseJSON[T any](r *http.Request, limit int64) (T, error) {
	var out T
	dec := json.NewDecoder(io.LimitReader(r.Body, limit))
	dec.DisallowUnknownFields()

	if err := dec.Decode(&out); err != nil {
		return out, err
	}

	// Ensure there's nothing else after the first JSON value
	if err := dec.Decode(new(any)); err != io.EOF {
		if err == nil {
			return out, fmt.Errorf("unexpected trailing data")
		}
		return out, err
	}

	return out, nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{
		"success": false,
		"error":   message,
		"code":    code,
	})
}

// validatePDFMagic checks that a file starts with %PDF (the PDF magic bytes).
// This catches cases where R2/S3 returns an XML error page, HTML, or other
// non-PDF content that would otherwise be misclassified by pdfinfo.
func validatePDFMagic(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open for validation: %w", err)
	}
	defer f.Close()

	header := make([]byte, 5)
	n, err := f.Read(header)
	if err != nil || n < 5 {
		return fmt.Errorf("downloaded file is too small to be a valid PDF")
	}

	if string(header[:4]) != "%PDF" {
		// Log the first bytes for debugging (safe — it's just magic bytes, not user data)
		preview := string(header[:n])
		if len(preview) > 40 {
			preview = preview[:40]
		}
		return fmt.Errorf("downloaded file is not a PDF (starts with %q) — presigned URL may be expired or invalid", preview)
	}
	return nil
}

func downloadPDFToTemp(ctx context.Context, url string, maxBytes int64, timeout time.Duration) (path string, cleanup func(), err error) {
	tmpDir, err := os.MkdirTemp("", "pdfproc-*")
	if err != nil {
		return "", nil, fmt.Errorf("temp dir: %w", err)
	}
	cleanup = func() { _ = os.RemoveAll(tmpDir) }

	outPath := filepath.Join(tmpDir, "doc.pdf")

	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	req.Header.Set("User-Agent", "pdfproc/1.0")

	client := &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			MaxIdleConns:        10,
			IdleConnTimeout:     30 * time.Second,
			DisableKeepAlives:   false,
			TLSHandshakeTimeout: 10 * time.Second,
		},
	}

	resp, err := client.Do(req)
	if err != nil {
		cleanup()
		return "", nil, fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		cleanup()
		return "", nil, fmt.Errorf("download failed: HTTP %d", resp.StatusCode)
	}

	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	if ct != "" && !strings.Contains(ct, "pdf") && !strings.Contains(ct, "octet-stream") {
		cleanup()
		return "", nil, fmt.Errorf("invalid content-type: %s", ct)
	}

	f, err := os.Create(outPath)
	if err != nil {
		cleanup()
		return "", nil, fmt.Errorf("create: %w", err)
	}
	defer f.Close()

	lr := &io.LimitedReader{R: resp.Body, N: maxBytes + 1}
	n, err := io.Copy(f, lr)
	if err != nil {
		cleanup()
		return "", nil, fmt.Errorf("write: %w", err)
	}
	if n > maxBytes {
		cleanup()
		return "", nil, fmt.Errorf("PDF exceeds %dMB limit", maxBytes/(1<<20))
	}
	if n < 100 {
		cleanup()
		return "", nil, fmt.Errorf("PDF too small (likely invalid)")
	}

	// Validate PDF magic bytes — catches R2 XML errors, HTML pages, etc.
	if err := validatePDFMagic(outPath); err != nil {
		cleanup()
		return "", nil, err
	}

	return outPath, cleanup, nil
}
