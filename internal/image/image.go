package image

import (
	"context"
	"errors"
	"regexp"
	"strings"

	"github.com/toricodesthings/file-processing-service/internal/ocr"
	"github.com/toricodesthings/file-processing-service/internal/types"
)

// Supported image extensions (matched case-insensitively).
var imageExtensions = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".gif": true,
	".webp": true, ".bmp": true, ".tiff": true, ".tif": true,
	".avif": true, ".svg": true,
}

// cleanOCRText applies light-touch cleaning to raw OCR output:
//   - Strips zero-width / invisible unicode characters
//   - Removes standalone image-filename lines
//   - Normalises line endings and collapses excessive blank lines
var (
	zeroWidthChars     = regexp.MustCompile("[\u200B-\u200D\uFEFF\u00AD\u2060]")
	standaloneImgName  = regexp.MustCompile(`(?mi)^[\w-]*(?:img|image|figure|fig|photo|pic)[\w-]*\.(jpeg|jpg|png|gif|webp|svg|bmp|tiff?)[ \t]*$`)
	standaloneFileName = regexp.MustCompile(`(?mi)^[\w-]+\.(jpeg|jpg|png|gif|webp|svg|bmp|tiff?)[ \t]*$`)
	excessiveNewlines  = regexp.MustCompile(`\n{4,}`)
	trailingSpaces     = regexp.MustCompile(`(?m)[ \t]+$`)
)

func cleanOCRText(text string) string {
	if text == "" {
		return ""
	}

	text = zeroWidthChars.ReplaceAllString(text, "")
	text = standaloneImgName.ReplaceAllString(text, "")
	text = standaloneFileName.ReplaceAllString(text, "")

	// Normalise line endings
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")

	text = trailingSpaces.ReplaceAllString(text, "")
	text = excessiveNewlines.ReplaceAllString(text, "\n\n\n")

	return strings.TrimSpace(text)
}

// ProcessImageOCR sends a publicly-accessible image URL to the Mistral OCR API
// and returns cleaned markdown text. The image is NOT downloaded to disk — the
// URL is passed directly to Mistral.
func ProcessImageOCR(ctx context.Context, imageURL string, model string) (types.ImageExtractionResult, error) {
	// Validate URL
	if strings.TrimSpace(imageURL) == "" {
		msg := "imageUrl required"
		return types.ImageExtractionResult{Error: &msg}, errors.New(msg)
	}

	lower := strings.ToLower(imageURL)
	if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") {
		msg := "imageUrl must be a valid HTTP/HTTPS URL"
		return types.ImageExtractionResult{Error: &msg}, errors.New(msg)
	}

	// Reject PDFs — those go through the PDF pipeline
	if strings.HasSuffix(lower, ".pdf") || strings.Contains(lower, ".pdf?") {
		msg := "PDF extraction is handled by the PDF service, not the image endpoint"
		return types.ImageExtractionResult{Error: &msg}, errors.New(msg)
	}

	if model == "" {
		model = "mistral-ocr-latest"
	}

	// Call Mistral OCR with image_url document type (no download required)
	ocrResp, err := ocr.RunMistralImageOCR(ctx, imageURL, model)
	if err != nil {
		msg := sanitiseOCRError(err)
		return types.ImageExtractionResult{Error: &msg}, err
	}

	if len(ocrResp.Pages) == 0 {
		msg := "no content extracted from image"
		return types.ImageExtractionResult{Error: &msg}, errors.New(msg)
	}

	// Combine page markdown (images typically produce 1 page)
	pageSep := "\n\n-----\n\n"
	var parts []string
	for _, p := range ocrResp.Pages {
		md := strings.TrimSpace(p.Markdown)
		if md == "" || md == "." {
			continue
		}
		parts = append(parts, md)
	}

	rawText := strings.Join(parts, pageSep)
	cleaned := cleanOCRText(rawText)

	return types.ImageExtractionResult{
		Success: true,
		Text:    cleaned,
	}, nil
}

// sanitiseOCRError produces a user-facing error message from OCR errors.
func sanitiseOCRError(err error) string {
	msg := err.Error()

	switch {
	case strings.Contains(msg, "404") || strings.Contains(msg, "not found"):
		return "Image URL not accessible (404)"
	case strings.Contains(msg, "403") || strings.Contains(msg, "forbidden"):
		return "Access denied to image URL"
	case strings.Contains(msg, "timeout"):
		return "Request timeout — try again later"
	case strings.Contains(msg, "network") || strings.Contains(msg, "ECONNREFUSED"):
		return "Network error — check connectivity"
	}

	if len(msg) > 300 {
		msg = msg[:300] + "..."
	}
	return msg
}
