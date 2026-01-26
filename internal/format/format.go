package format

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/toricodesthings/PDF-to-Text-Extraction-Service/internal/types"
)

var (
	// Patterns for cleaning
	multipleBlankLines = regexp.MustCompile(`\n{3,}`)
	imagePattern       = regexp.MustCompile(`!\[([^\]]*)\]\([^\)]+\)`) // ![alt](url)
	htmlCommentPattern = regexp.MustCompile(`<!--.*?-->`)
)

// Combine merges page results into clean, standardized markdown suitable for vectorization
func Combine(pages []types.PageExtractionResult, sep string, includePageNums bool) string {
	var parts []string

	for _, p := range pages {
		txt := normalizeMarkdown(p.Text)
		if txt == "" {
			continue
		}

		// Strip image placeholders (not useful for RAG/vectorization)
		txt = stripImages(txt)

		// Convert HTML tables to markdown (better for chunking)
		txt = convertHTMLTables(txt)

		// Add page marker if requested (as plain text, not HTML)
		if includePageNums {
			parts = append(parts, fmt.Sprintf("[Page %d]\n\n%s", p.PageNumber, txt))
		} else {
			parts = append(parts, txt)
		}
	}

	if len(parts) == 0 {
		return ""
	}

	// Always use --- separator for clear page boundaries (good for chunking)
	if sep == "" {
		sep = "\n\n---\n\n"
	}

	combined := strings.Join(parts, sep)
	return finalCleanup(combined)
}

// stripImages removes image placeholders which aren't useful for text search/RAG
func stripImages(text string) string {
	// Remove ![alt](url) patterns
	text = imagePattern.ReplaceAllString(text, "")

	// Remove any HTML <img> tags that might be present
	text = regexp.MustCompile(`<img[^>]*>`).ReplaceAllString(text, "")

	return text
}

// convertHTMLTables converts HTML tables to markdown for better chunking
func convertHTMLTables(text string) string {
	// Simple HTML table to markdown conversion
	// This handles basic tables - complex tables with rowspan/colspan
	// will be converted to simplified markdown

	lines := strings.Split(text, "\n")
	var result []string
	inTable := false
	var tableRows [][]string

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "<table") {
			inTable = true
			tableRows = [][]string{}
			continue
		}

		if strings.HasPrefix(trimmed, "</table>") {
			if len(tableRows) > 0 {
				// Convert accumulated rows to markdown table
				mdTable := buildMarkdownTable(tableRows)
				result = append(result, mdTable)
			}
			inTable = false
			tableRows = [][]string{}
			continue
		}

		if inTable {
			// Extract cells from <tr> rows
			if strings.Contains(trimmed, "<tr>") || strings.Contains(trimmed, "<th>") || strings.Contains(trimmed, "<td>") {
				cells := extractTableCells(line)
				if len(cells) > 0 {
					tableRows = append(tableRows, cells)
				}
			}
			continue
		}

		result = append(result, line)
	}

	return strings.Join(result, "\n")
}

// extractTableCells extracts text from <th> and <td> tags
func extractTableCells(line string) []string {
	// Remove <tr> tags
	line = strings.ReplaceAll(line, "<tr>", "")
	line = strings.ReplaceAll(line, "</tr>", "")

	var cells []string

	// Extract from <th> tags (headers)
	thPattern := regexp.MustCompile(`<th[^>]*>(.*?)</th>`)
	matches := thPattern.FindAllStringSubmatch(line, -1)
	for _, match := range matches {
		if len(match) > 1 {
			cell := strings.TrimSpace(stripHTMLTags(match[1]))
			cells = append(cells, cell)
		}
	}

	// Extract from <td> tags (data)
	tdPattern := regexp.MustCompile(`<td[^>]*>(.*?)</td>`)
	matches = tdPattern.FindAllStringSubmatch(line, -1)
	for _, match := range matches {
		if len(match) > 1 {
			cell := strings.TrimSpace(stripHTMLTags(match[1]))
			cells = append(cells, cell)
		}
	}

	return cells
}

// stripHTMLTags removes HTML tags from text
func stripHTMLTags(text string) string {
	// Remove all HTML tags
	text = regexp.MustCompile(`<[^>]+>`).ReplaceAllString(text, "")
	// Decode common HTML entities
	text = strings.ReplaceAll(text, "&nbsp;", " ")
	text = strings.ReplaceAll(text, "&lt;", "<")
	text = strings.ReplaceAll(text, "&gt;", ">")
	text = strings.ReplaceAll(text, "&amp;", "&")
	return text
}

// buildMarkdownTable creates a markdown table from rows
func buildMarkdownTable(rows [][]string) string {
	if len(rows) == 0 {
		return ""
	}

	// Determine column count
	maxCols := 0
	for _, row := range rows {
		if len(row) > maxCols {
			maxCols = len(row)
		}
	}

	if maxCols == 0 {
		return ""
	}

	// Normalize all rows to have same column count
	normalizedRows := make([][]string, len(rows))
	for i, row := range rows {
		normalizedRows[i] = make([]string, maxCols)
		for j := 0; j < maxCols; j++ {
			if j < len(row) {
				normalizedRows[i][j] = row[j]
			} else {
				normalizedRows[i][j] = ""
			}
		}
	}

	var result strings.Builder

	// Header row
	result.WriteString("| " + strings.Join(normalizedRows[0], " | ") + " |\n")

	// Separator row
	separators := make([]string, maxCols)
	for i := 0; i < maxCols; i++ {
		separators[i] = "---"
	}
	result.WriteString("| " + strings.Join(separators, " | ") + " |\n")

	// Data rows
	for i := 1; i < len(normalizedRows); i++ {
		result.WriteString("| " + strings.Join(normalizedRows[i], " | ") + " |\n")
	}

	return result.String()
}

// normalizeMarkdown cleans and standardizes markdown while preserving structure
func normalizeMarkdown(text string) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}

	// Step 1: Normalize line endings
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")

	// Step 2: Process line by line to preserve markdown structure
	lines := strings.Split(text, "\n")
	var cleaned []string

	inCodeBlock := false
	prevWasBlank := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Detect code blocks (preserve exactly)
		if strings.HasPrefix(trimmed, "```") {
			inCodeBlock = !inCodeBlock
			cleaned = append(cleaned, line)
			prevWasBlank = false
			continue
		}

		// Inside code blocks: preserve exactly
		if inCodeBlock {
			cleaned = append(cleaned, line)
			prevWasBlank = false
			continue
		}

		// Clean the line
		line = cleanLine(line)

		// Handle blank lines (max 1 consecutive)
		if line == "" {
			if !prevWasBlank {
				cleaned = append(cleaned, "")
			}
			prevWasBlank = true
			continue
		}

		prevWasBlank = false
		cleaned = append(cleaned, line)
	}

	return strings.TrimSpace(strings.Join(cleaned, "\n"))
}

// cleanLine cleans a single line while preserving markdown syntax
func cleanLine(line string) string {
	// Remove trailing whitespace
	line = strings.TrimRight(line, " \t")

	// Handle markdown headers (preserve spacing after #)
	if strings.HasPrefix(line, "#") {
		line = normalizeHeader(line)
		return line
	}

	// Handle bullet points (preserve structure)
	trimmed := strings.TrimLeft(line, " \t")
	if len(trimmed) > 0 {
		firstChar := trimmed[0]
		// Bullets: -, *, +
		if firstChar == '-' || firstChar == '*' || firstChar == '+' {
			indent := len(line) - len(trimmed)
			if len(trimmed) > 1 && trimmed[1] == ' ' {
				return line
			}
			return strings.Repeat(" ", indent) + string(firstChar) + " " + trimmed[1:]
		}
	}

	// Handle numbered lists
	if matched, _ := regexp.MatchString(`^\d+\.`, trimmed); matched {
		parts := strings.SplitN(trimmed, ".", 2)
		if len(parts) == 2 {
			remainder := strings.TrimLeft(parts[1], " ")
			indent := len(line) - len(trimmed)
			return strings.Repeat(" ", indent) + parts[0] + ". " + remainder
		}
	}

	return line
}

// normalizeHeader ensures proper spacing in markdown headers
func normalizeHeader(line string) string {
	hashCount := 0
	for i := 0; i < len(line) && line[i] == '#'; i++ {
		hashCount++
	}

	if hashCount == 0 || hashCount > 6 {
		return line
	}

	rest := strings.TrimLeft(line[hashCount:], " \t")
	if rest == "" {
		return line
	}

	return strings.Repeat("#", hashCount) + " " + rest
}

// finalCleanup performs final passes for vectorization-ready output
func finalCleanup(text string) string {
	// Remove HTML comments (not useful for RAG)
	text = htmlCommentPattern.ReplaceAllString(text, "")

	// Collapse excessive blank lines (max 2 for paragraph breaks)
	text = multipleBlankLines.ReplaceAllString(text, "\n\n")

	// Ensure proper spacing around headers for better chunking
	text = ensureHeaderSpacing(text)

	// Ensure proper spacing around code blocks
	text = ensureCodeBlockSpacing(text)

	// Final trim
	return strings.TrimSpace(text)
}

// ensureHeaderSpacing ensures blank line before headers (better chunk boundaries)
func ensureHeaderSpacing(text string) string {
	lines := strings.Split(text, "\n")
	var result []string

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Check if this is a header
		if strings.HasPrefix(trimmed, "#") && len(trimmed) > 1 {
			// If not at start and previous line isn't blank, add blank line
			if i > 0 && len(result) > 0 && result[len(result)-1] != "" {
				result = append(result, "")
			}
		}

		result = append(result, line)
	}

	return strings.Join(result, "\n")
}

// ensureCodeBlockSpacing ensures blank lines around code blocks
func ensureCodeBlockSpacing(text string) string {
	lines := strings.Split(text, "\n")
	var result []string

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "```") {
			// Add blank line before opening ```
			if i > 0 && len(result) > 0 && result[len(result)-1] != "" {
				result = append(result, "")
			}

			result = append(result, line)

			// Check if this is closing ``` and add blank after
			if i > 0 && !strings.HasPrefix(strings.TrimSpace(lines[i-1]), "```") {
				// This is closing ```, add blank after (will be added in next iteration)
			}
			continue
		}

		result = append(result, line)
	}

	return strings.Join(result, "\n")
}
