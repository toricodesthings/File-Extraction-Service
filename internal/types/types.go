package types

type HybridProcessorOptions struct {
	MinWordsThreshold  int     `json:"minWordsThreshold"`
	PageSeparator      string  `json:"pageSeparator"`
	IncludePageNumbers bool    `json:"includePageNumbers"`
	OCRTriggerRatio    float64 `json:"ocrTriggerRatio"`
	Pages              []int   `json:"pages"`

	ExtractHeader bool    `json:"extractHeader"`
	ExtractFooter bool    `json:"extractFooter"`
	OCRModel      *string `json:"ocrModel"`

	// Preview-only knobs (text-layer only)
	PreviewMaxPages int `json:"previewMaxPages"` // default e.g. 8
	PreviewMaxChars int `json:"previewMaxChars"` // default e.g. 20000
}

type ExtractRequest struct {
	PresignedURL string                 `json:"presignedUrl"`
	Options      HybridProcessorOptions `json:"options"`
}

type PageExtractionResult struct {
	PageNumber int    `json:"pageNumber"`
	Text       string `json:"text"`
	Method     string `json:"method"` // "text-layer" | "ocr"
	WordCount  int    `json:"wordCount"`
}

type HybridExtractionResult struct {
	Success            bool                   `json:"success"`
	Text               string                 `json:"text"`
	Pages              []PageExtractionResult `json:"pages"`
	TotalPages         int                    `json:"totalPages"`
	TextLayerPages     int                    `json:"textLayerPages"`
	OCRPages           int                    `json:"ocrPages"`
	CostSavingsPercent int                    `json:"costSavingsPercent"`
	Error              *string                `json:"error,omitempty"`
}

type PreviewResult struct {
	Success        bool    `json:"success"`
	NeedsOCR       bool    `json:"needsOcr"`
	Text           string  `json:"text"`
	WordCount      int     `json:"wordCount"`
	TotalPages     int     `json:"totalPages"`
	TextLayerPages int     `json:"textLayerPages"`
	Error          *string `json:"error,omitempty"`
}

// ── Image extraction types ───────────────────────────────────────────────────

type ImageExtractRequest struct {
	ImageURL string `json:"imageUrl"`
}

type ImageExtractionResult struct {
	Success     bool    `json:"success"`
	Text        string  `json:"text"`                  // Primary text for embedding (OCR transcription OR vision description)
	Method      string  `json:"method,omitempty"`      // "ocr" | "vision" | "ocr+vision"
	ImageType   string  `json:"imageType,omitempty"`   // "handwriting" | "photo" | "diagram" | etc.
	Description string  `json:"description,omitempty"` // Vision-generated description (present when vision ran)
	Error       *string `json:"error,omitempty"`
}
