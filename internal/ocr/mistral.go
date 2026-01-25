package ocr

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
)

type OCRPage struct {
	Index    int    `json:"index"`    // 0-indexed
	Markdown string `json:"markdown"` // extracted markdown
}

type OCRResponse struct {
	Pages []OCRPage `json:"pages"`
}

func RunMistralOCR(ctx context.Context, presignedURL string, model string, pages0 []int, extractHeader, extractFooter bool) (OCRResponse, error) {
	key := os.Getenv("MISTRAL_API_KEY")
	if key == "" {
		return OCRResponse{}, fmt.Errorf("missing MISTRAL_API_KEY")
	}

	// Normalize pages (sorted unique)
	if len(pages0) > 0 {
		sort.Ints(pages0)
		pages0 = uniqueInts(pages0)
	}

	body := map[string]any{
		"model": model,
		"document": map[string]any{
			"type":        "document_url",
			"documentUrl":  presignedURL,
		},
		"extract_header": extractHeader,
		"extract_footer": extractFooter,
	}
	if len(pages0) > 0 {
		body["pages"] = pages0
	}

	b, _ := json.Marshal(body)

	req, _ := http.NewRequestWithContext(ctx, "POST", "https://api.mistral.ai/v1/ocr", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return OCRResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		slurp, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		return OCRResponse{}, fmt.Errorf("mistral ocr error %d: %s", resp.StatusCode, string(slurp))
	}

	var parsed OCRResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return OCRResponse{}, err
	}
	return parsed, nil
}

func uniqueInts(xs []int) []int {
	out := make([]int, 0, len(xs))
	var last *int
	for _, x := range xs {
		if last == nil || *last != x {
			out = append(out, x)
			v := x
			last = &v
		}
	}
	return out
}
