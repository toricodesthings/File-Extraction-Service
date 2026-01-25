package format

import (
	"fmt"
	"strings"

	"github.com/toricodesthings/PDF-to-Text-Extraction-Service/internal/types"
)

func Combine(pages []types.PageExtractionResult, sep string, includePageNums bool) string {
	var b strings.Builder
	first := true
	for _, p := range pages {
		txt := strings.TrimSpace(p.Text)
		if txt == "" {
			continue
		}
		if !first {
			b.WriteString(sep)
		}
		first = false
		if includePageNums {
			b.WriteString(fmt.Sprintf("## Page %d\n\n", p.PageNumber))
		}
		b.WriteString(txt)
	}
	return strings.TrimSpace(b.String())
}
