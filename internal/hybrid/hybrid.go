package hybrid

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/toricodesthings/PDF-to-Text-Extraction-Service/internal/extractor"
	"github.com/toricodesthings/PDF-to-Text-Extraction-Service/internal/format"
	"github.com/toricodesthings/PDF-to-Text-Extraction-Service/internal/ocr"
	"github.com/toricodesthings/PDF-to-Text-Extraction-Service/internal/quality"
	"github.com/toricodesthings/PDF-to-Text-Extraction-Service/internal/types"
)

type PageEval struct {
	Page     int
	Text     string
	Decision quality.Decision
}

func WithDefaults(o types.HybridProcessorOptions) types.HybridProcessorOptions {
	if o.MinWordsThreshold == 0 {
		o.MinWordsThreshold = 10
	}
	if o.PageSeparator == "" {
		o.PageSeparator = "\n\n---\n\n"
	}
	if o.OCRTriggerRatio == 0 {
		o.OCRTriggerRatio = 0.3
	}
	// default true
	if !o.IncludePageNumbers {
		o.IncludePageNumbers = true
	}
	return o
}

func ProcessHybrid(ctx context.Context, presignedURL, pdfPath string, opts types.HybridProcessorOptions) (types.HybridExtractionResult, error) {
	opts = WithDefaults(opts)

	totalPages, err := extractor.PageCount(ctx, pdfPath)
	if err != nil || totalPages <= 0 {
		return fail(fmt.Sprintf("page count failed: %v", err)), nil
	}

	selected := normalizePages(opts.Pages, totalPages)
	pagesToInclude := selected
	if pagesToInclude == nil {
		pagesToInclude = make([]int, totalPages)
		for i := 1; i <= totalPages; i++ {
			pagesToInclude[i-1] = i
		}
	}

	// 1) text-layer per page + quality decision
	evals := make([]PageEval, 0, len(pagesToInclude))
	needsOCR := make([]int, 0)

	for _, p := range pagesToInclude {
		raw, _ := extractor.TextForPage(ctx, pdfPath, p)
		raw = cleanText(raw)
		d := quality.Score(raw, opts.MinWordsThreshold)

		evals = append(evals, PageEval{Page: p, Text: raw, Decision: d})
		if d.NeedsOCR {
			needsOCR = append(needsOCR, p)
		}
	}

	ocrRatio := 0.0
	if len(pagesToInclude) > 0 {
		ocrRatio = float64(len(needsOCR)) / float64(len(pagesToInclude))
	}

	// 2) Decide OCR strategy:
	// - If too many pages need OCR -> OCR all selected pages (or whole doc)
	// - Else OCR only those pages
	var pagesForOCR0 []int
	if len(needsOCR) > 0 {
		if ocrRatio > opts.OCRTriggerRatio {
			// OCR everything we’re returning (selected pages)
			pagesForOCR0 = toZeroIndexed(pagesToInclude)
		} else {
			// OCR only bad pages (best case)
			pagesForOCR0 = toZeroIndexed(needsOCR)
		}
	}

	// 3) If OCR needed, call Mistral once, merge results
	ocrMarkdownByPage := map[int]string{}
	if len(pagesForOCR0) > 0 {
		model := "mistral-ocr-latest"
		if opts.OCRModel != nil && *opts.OCRModel != "" {
			model = *opts.OCRModel
		}
		resp, err := ocr.RunMistralOCR(ctx, presignedURL, model, pagesForOCR0, opts.ExtractHeader, opts.ExtractFooter)
		if err != nil {
			return fail("ocr failed: " + err.Error()), nil
		}
		for _, pg := range resp.Pages {
			oneIndexed := pg.Index + 1
			ocrMarkdownByPage[oneIndexed] = cleanOCR(pg.Markdown)
		}
	}

	// 4) Build final pages in order
	out := make([]types.PageExtractionResult, 0, len(pagesToInclude))
	ocrCount := 0
	for _, ev := range evals {
		if md, ok := ocrMarkdownByPage[ev.Page]; ok && strings.TrimSpace(md) != "" {
			out = append(out, types.PageExtractionResult{
				PageNumber: ev.Page,
				Text:       md,
				Method:     "ocr",
				WordCount:  quality.CountWords(md),
			})
			ocrCount++
		} else {
			out = append(out, types.PageExtractionResult{
				PageNumber: ev.Page,
				Text:       ev.Text,
				Method:     "text-layer",
				WordCount:  ev.Decision.WordCount,
			})
		}
	}

	combined := format.Combine(out, opts.PageSeparator, opts.IncludePageNumbers)

	// Savings estimate: proportion of pages not OCR’d
	savings := 0
	if len(out) > 0 {
		savings = int((1.0 - float64(ocrCount)/float64(len(out))) * 100.0)
	}

	return types.HybridExtractionResult{
		Success:            true,
		Text:               combined,
		Pages:              out,
		TotalPages:         totalPages,
		TextLayerPages:     len(out) - ocrCount,
		OCRPages:           ocrCount,
		CostSavingsPercent: savings,
	}, nil
}

func ProcessPreview(ctx context.Context, pdfPath string, opts types.HybridProcessorOptions) types.PreviewResult {
	opts = WithDefaults(opts)

	totalPages, err := extractor.PageCount(ctx, pdfPath)
	if err != nil || totalPages <= 0 {
		msg := "page count failed"
		return types.PreviewResult{Success: false, NeedsOCR: true, Error: &msg}
	}

	selected := normalizePages(opts.Pages, totalPages)
	pagesToInclude := selected
	if pagesToInclude == nil {
		pagesToInclude = make([]int, totalPages)
		for i := 1; i <= totalPages; i++ {
			pagesToInclude[i-1] = i
		}
	}

	needs := 0
	textLayerPages := 0
	for _, p := range pagesToInclude {
		raw, _ := extractor.TextForPage(ctx, pdfPath, p)
		raw = cleanText(raw)
		d := quality.Score(raw, opts.MinWordsThreshold)
		if d.NeedsOCR {
			needs++
		} else {
			textLayerPages++
		}
	}
	ratio := 0.0
	if len(pagesToInclude) > 0 {
		ratio = float64(needs) / float64(len(pagesToInclude))
	}

	// Preview only returns “needs OCR?” without OCR call
	return types.PreviewResult{
		Success:        true,
		NeedsOCR:       needs > 0 && ratio > opts.OCRTriggerRatio,
		Text:           "",
		WordCount:      0,
		TotalPages:     totalPages,
		TextLayerPages: textLayerPages,
	}
}

// --- helpers ---

func fail(msg string) types.HybridExtractionResult {
	return types.HybridExtractionResult{Success: false, Error: &msg}
}

func toZeroIndexed(pages1 []int) []int {
	out := make([]int, 0, len(pages1))
	for _, p := range pages1 {
		out = append(out, p-1)
	}
	sort.Ints(out)
	return out
}

func normalizePages(req []int, total int) []int {
	if len(req) == 0 || total <= 0 {
		return nil
	}
	seen := map[int]bool{}
	out := make([]int, 0, len(req))
	for _, n := range req {
		if n < 1 || n > total {
			continue
		}
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	sort.Ints(out)
	if len(out) == 0 {
		return nil
	}
	return out
}

func cleanText(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = strings.TrimSpace(s)
	return s
}

func cleanOCR(s string) string {
	// keep mild normalization
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return strings.TrimSpace(s)
}

func requireEnv(key string) error {
	if os.Getenv(key) == "" {
		return fmt.Errorf("missing env %s", key)
	}
	return nil
}
