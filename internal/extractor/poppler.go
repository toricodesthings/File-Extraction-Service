package extractor

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type ExtractorConfig struct {
	PDFInfoTimeout      time.Duration
	PDFToTextTimeout    time.Duration
	PDFToTextAllTimeout time.Duration
}

var (
	pageCountRegex = regexp.MustCompile(`(?m)^Pages:\s+(\d+)\s*$`)
)

// PageCount extracts the total number of pages using pdfinfo
func PageCount(ctx context.Context, pdfPath string, cfg ExtractorConfig) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, cfg.PDFInfoTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "pdfinfo", pdfPath)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		// Provide more context on failure
		if ctx.Err() == context.DeadlineExceeded {
			return 0, fmt.Errorf("pdfinfo timeout: %w", ctx.Err())
		}
		errMsg := strings.TrimSpace(stderr.String())
		if errMsg != "" {
			return 0, fmt.Errorf("pdfinfo failed: %s", errMsg)
		}
		return 0, fmt.Errorf("pdfinfo failed: %w", err)
	}

	output := stdout.String()
	matches := pageCountRegex.FindStringSubmatch(output)
	if len(matches) != 2 {
		return 0, fmt.Errorf("pdfinfo: pages field not found in output")
	}

	count, err := strconv.Atoi(matches[1])
	if err != nil {
		return 0, fmt.Errorf("pdfinfo: invalid page count: %w", err)
	}

	if count <= 0 || count > 50000 {
		return 0, fmt.Errorf("pdfinfo: unreasonable page count: %d", count)
	}

	return count, nil
}

// TextForPage extracts text from a specific page using pdftotext
func TextForPage(ctx context.Context, pdfPath string, page int) (string, error) {
	if page < 1 {
		return "", fmt.Errorf("invalid page number: %d (must be >= 1)", page)
	}

	// Per-page extraction should be fast, but allow some buffer
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx,
		"pdftotext",
		"-f", strconv.Itoa(page),
		"-l", strconv.Itoa(page),
		"-layout",       // Preserve layout
		"-nopgbrk",      // No page breaks
		"-enc", "UTF-8", // Force UTF-8 encoding
		pdfPath,
		"-", // Output to stdout
	)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("pdftotext timeout on page %d", page)
		}
		errMsg := strings.TrimSpace(stderr.String())
		if errMsg != "" {
			// Common errors: encrypted PDFs, corrupted pages
			if strings.Contains(errMsg, "Incorrect password") {
				return "", fmt.Errorf("PDF is password protected")
			}
			if strings.Contains(errMsg, "PDF file is damaged") {
				return "", fmt.Errorf("PDF file is damaged or corrupted")
			}
			return "", fmt.Errorf("pdftotext page %d failed: %s", page, errMsg)
		}
		return "", fmt.Errorf("pdftotext page %d failed: %w", page, err)
	}

	text := stdout.String()

	// Validate extracted text
	if len(text) > 10<<20 { // 10 MB limit per page (sanity check)
		return "", fmt.Errorf("extracted text too large: %d bytes", len(text))
	}

	return text, nil
}

// ExtractAllPages extracts text from all pages at once (batch mode)
// This can be faster for small documents but less resilient to errors
func ExtractAllPages(ctx context.Context, pdfPath string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx,
		"pdftotext",
		"-layout",
		"-nopgbrk",
		"-enc", "UTF-8",
		pdfPath,
		"-",
	)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("pdftotext timeout")
		}
		errMsg := strings.TrimSpace(stderr.String())
		if errMsg != "" {
			return "", fmt.Errorf("pdftotext failed: %s", errMsg)
		}
		return "", fmt.Errorf("pdftotext failed: %w", err)
	}

	text := stdout.String()

	if len(text) > 50<<20 { // 50 MB total limit
		return "", fmt.Errorf("extracted text too large: %d bytes", len(text))
	}

	return text, nil
}

// ValidatePDF performs basic validation using pdfinfo
func ValidatePDF(ctx context.Context, pdfPath string) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "pdfinfo", pdfPath)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		errMsg := strings.TrimSpace(stderr.String())
		if strings.Contains(errMsg, "Incorrect password") {
			return fmt.Errorf("PDF is password protected")
		}
		if strings.Contains(errMsg, "damaged") || strings.Contains(errMsg, "invalid") {
			return fmt.Errorf("PDF appears to be damaged or invalid")
		}
		return fmt.Errorf("PDF validation failed: %s", errMsg)
	}

	return nil
}
