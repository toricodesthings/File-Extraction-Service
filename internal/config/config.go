package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	// Server
	Port string

	// Secrets
	InternalSharedSecret string
	MistralAPIKey        string
	OpenRouterAPIKey     string

	// Limits
	MaxJSONBodyBytes int64
	MaxPDFBytes      int64

	// Concurrency
	MaxConcurrentRequests int64
	MaxOCRConcurrent      int64
	MaxPageWorkers        int // per-document page extraction workers cap

	// Server timeouts
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration

	// Request timeouts
	ExtractTimeout      time.Duration
	PreviewTimeout      time.Duration
	ImageExtractTimeout time.Duration

	// Download
	DownloadTimeout time.Duration

	// Poppler / extraction timeouts
	PDFInfoTimeout      time.Duration
	PDFToTextTimeout    time.Duration
	PDFToTextAllTimeout time.Duration

	// rate limiting (per IP)
	RateLimitEvery time.Duration
	RateLimitBurst int

	// housekeeping
	CleanupInterval time.Duration

	// health
	HealthDegradeRatio float64

	// http
	MaxHeaderBytes int
	MaxImageURLLen int

	// Hybrid defaults (used when request options omit values)
	DefaultMinWordsThreshold    int
	DefaultOCRTriggerRatio      float64
	DefaultPageSeparator        string
	DefaultOCRModel             string
	DefaultPreviewMaxPages      int
	DefaultPreviewMaxChars      int
	DefaultPreviewNeedsOCRRatio float64

	// Vision (OpenRouter) defaults
	DefaultVisionModel   string
	VisionRequestTimeout time.Duration
}

func Load() Config {
	return Config{
		Port: envStr("PORT", "8080"),

		InternalSharedSecret: envStr("INTERNAL_SHARED_SECRET", ""),
		MistralAPIKey:        envStr("MISTRAL_API_KEY", ""),
		OpenRouterAPIKey:     envStr("OPENROUTER_API_KEY", ""),

		MaxJSONBodyBytes: int64(envInt("MAX_JSON_BODY_BYTES", 2<<20)),
		MaxPDFBytes:      int64(envInt("MAX_PDF_BYTES", int(200<<20))),

		MaxConcurrentRequests: int64(envInt("MAX_CONCURRENT_REQUESTS", 15)),
		MaxOCRConcurrent:      int64(envInt("MAX_OCR_CONCURRENT", 3)),
		MaxPageWorkers:        envInt("MAX_PAGE_WORKERS", 8),

		ReadHeaderTimeout: envDur("READ_HEADER_TIMEOUT", 10*time.Second),
		ReadTimeout:       envDur("READ_TIMEOUT", 30*time.Second),
		WriteTimeout:      envDur("WRITE_TIMEOUT", 180*time.Second),
		IdleTimeout:       envDur("IDLE_TIMEOUT", 60*time.Second),

		ExtractTimeout:      envDur("EXTRACT_TIMEOUT", 160*time.Second),
		PreviewTimeout:      envDur("PREVIEW_TIMEOUT", 60*time.Second),
		ImageExtractTimeout: envDur("IMAGE_EXTRACT_TIMEOUT", 120*time.Second),

		DownloadTimeout: envDur("DOWNLOAD_TIMEOUT", 25*time.Second),

		PDFInfoTimeout:      envDur("PDFINFO_TIMEOUT", 5*time.Second),
		PDFToTextTimeout:    envDur("PDFTOTEXT_TIMEOUT", 10*time.Second),
		PDFToTextAllTimeout: envDur("PDFTOTEXT_ALL_TIMEOUT", 30*time.Second),

		RateLimitEvery: envDur("RATE_LIMIT_EVERY", 600*time.Millisecond),
		RateLimitBurst: envInt("RATE_LIMIT_BURST", 20),

		CleanupInterval: envDur("CLEANUP_INTERVAL", 5*time.Minute),

		HealthDegradeRatio: envFloat("HEALTH_DEGRADE_RATIO", 0.9),

		MaxHeaderBytes: envInt("MAX_HEADER_BYTES", 1<<20),
		MaxImageURLLen: envInt("MAX_IMAGE_URL_LEN", 2048),

		DefaultMinWordsThreshold:    envInt("DEFAULT_MIN_WORDS", 20),
		DefaultOCRTriggerRatio:      envFloat("DEFAULT_OCR_TRIGGER_RATIO", 0.25),
		DefaultPageSeparator:        envStr("DEFAULT_PAGE_SEPARATOR", "\n\n---\n\n"),
		DefaultOCRModel:             envStr("DEFAULT_OCR_MODEL", "mistral-ocr-latest"),
		DefaultPreviewMaxPages:      envInt("DEFAULT_PREVIEW_PAGES", 8),
		DefaultPreviewMaxChars:      envInt("DEFAULT_PREVIEW_CHARS", 20000),
		DefaultPreviewNeedsOCRRatio: envFloat("DEFAULT_PREVIEW_NEEDS_OCR_RATIO", 0.25),

		DefaultVisionModel:   envStr("DEFAULT_VISION_MODEL", "mistralai/mistral-small-3.1-24b-instruct"),
		VisionRequestTimeout: envDur("VISION_REQUEST_TIMEOUT", 30*time.Second),
	}
}

func (c Config) Validate() error {
	if len(strings.TrimSpace(c.InternalSharedSecret)) < 32 {
		return fmt.Errorf("INTERNAL_SHARED_SECRET must be at least 32 characters")
	}
	return nil
}

func envStr(key, fallback string) string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	return v
}

func envInt(key string, fallback int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}

func envFloat(key string, fallback float64) float64 {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || f <= 0 {
		return fallback
	}
	return f
}

func envDur(key string, fallback time.Duration) time.Duration {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return fallback
	}
	return d
}
