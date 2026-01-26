package quality

import (
	"math"
	"strings"
	"unicode"
)

type Decision struct {
	Quality   float64
	NeedsOCR  bool
	MaybeOCR  bool
	Reasons   []string
	WordCount int
}

func CountWords(s string) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	return len(strings.Fields(s))
}

func Score(text string, minWords int) Decision {
	clean := normalize(text)
	wc := CountWords(clean)

	// Comprehensive feature extraction
	total := float64(len([]rune(clean)))
	if total == 0 {
		return Decision{
			Quality:   0,
			NeedsOCR:  true,
			Reasons:   []string{"empty_text"},
			WordCount: 0,
		}
	}

	alpha := float64(countIf(clean, unicode.IsLetter))
	digits := float64(countIf(clean, unicode.IsDigit))
	punct := float64(countIf(clean, unicode.IsPunct))
	spaces := float64(countIf(clean, unicode.IsSpace))
	garbage := float64(countGarbage(clean))

	alphaRatio := safeDiv(alpha, total)
	digitRatio := safeDiv(digits, total)
	punctRatio := safeDiv(punct, total)
	spaceRatio := safeDiv(spaces, total)
	garbageRatio := safeDiv(garbage, total)

	lines := splitLines(clean)
	lineCount := len(lines)
	avgLineLen, shortLineRatio := lineStats(lines)

	uniqueWordRatio := uniqueWordRatio(clean)
	repeatedChars := hasRepeatedCharPatterns(clean)
	scrambledRatio := countScrambledRatio(clean)

	// New: Detect structured content (cheat sheets, notes)
	bulletRatio := countBulletPointRatio(lines)
	hasEquations := detectEquationLikeContent(clean)

	// Scoring system with weighted penalties
	score := 1.0
	reasons := []string{}

	// Word count signal - MORE LENIENT for structured docs
	// Cheat sheets might have < 20 words but still be valid
	if wc < minWords {
		// Reduce penalty if we detect structured content
		penalty := 0.45
		if wc < minWords/2 {
			penalty = 0.60
		}
		// Structured content (bullets, equations) gets less penalty
		if bulletRatio > 0.3 || hasEquations {
			penalty *= 0.5 // Cut penalty in half
		}
		score -= penalty
		reasons = append(reasons, "low_word_count")
	}

	// Alpha ratio - ADJUSTED for technical documents
	// Research papers, homework, cheat sheets have more symbols/numbers
	if alphaRatio < 0.25 { // Reduced from 0.35
		penalty := 0.35
		if alphaRatio < 0.15 { // Reduced from 0.20
			penalty = 0.50
		}
		// If high digit ratio, it's likely math-heavy (OK)
		if digitRatio > 0.20 {
			penalty *= 0.6
		}
		score -= penalty
		reasons = append(reasons, "low_alpha_ratio")
	}

	// Garbage characters - STRICT (always bad)
	if garbageRatio > 0.01 {
		penalty := math.Min(0.50, garbageRatio*50)
		score -= penalty
		reasons = append(reasons, "garbage_chars")
	}

	// Fragmentation detection - IMPROVED for structured docs
	// Lecture notes and cheat sheets naturally have short lines
	if lineCount > 0 && shortLineRatio > 0.75 && avgLineLen < 12 && alphaRatio < 0.40 {
		// Very extreme threshold - most structured docs won't hit this
		score -= 0.25
		reasons = append(reasons, "fragmented_lines")
	}

	// Repetitive content - ADJUSTED for lecture notes
	// Headers/footers in slides repeat, that's normal
	if wc > 50 && uniqueWordRatio < 0.20 { // Increased threshold from 30 words
		score -= 0.15
		reasons = append(reasons, "low_unique_words")
	}

	// Repeated character patterns - keep as is
	if repeatedChars {
		score -= 0.20
		reasons = append(reasons, "repeated_patterns")
	}

	// Scrambled text detection - ADJUSTED
	// Some abbreviations are OK in notes
	if scrambledRatio > 0.30 { // Increased from 0.25
		score -= 0.25
		reasons = append(reasons, "scrambled_text")
	}

	// Excessive punctuation - ADJUSTED for code/equations
	// Code snippets and equations have lots of symbols
	if punctRatio > 0.50 && alphaRatio < 0.20 { // More lenient
		score -= 0.20
		reasons = append(reasons, "excessive_punctuation")
	}

	// Space ratio anomaly - keep improved threshold
	if spaceRatio > 0.60 || (wc > 10 && spaceRatio < 0.05) {
		score -= 0.15
		reasons = append(reasons, "abnormal_spacing")
	}

	// Positive signals

	// Numeric content (tables, equations, homework)
	if digitRatio > 0.25 && alphaRatio > 0.15 && wc >= minWords/2 {
		score += 0.10
		reasons = append(reasons, "numeric_heavy")
	}

	// Good prose (articles, books, papers)
	if alphaRatio > 0.60 && wc >= minWords && uniqueWordRatio > 0.30 {
		score += 0.10
		reasons = append(reasons, "good_prose")
	}

	// Structured content (notes, cheat sheets, homework)
	if bulletRatio > 0.2 || hasEquations {
		score += 0.15
		reasons = append(reasons, "structured_content")
	}

	// Mixed content (research papers with text + equations)
	if alphaRatio > 0.40 && digitRatio > 0.10 && wc >= minWords {
		score += 0.10
		reasons = append(reasons, "mixed_content")
	}

	score = clamp(score, 0, 1)

	// Decision bands - slightly adjusted
	needs := score < 0.50 // Reduced from 0.55 to be more lenient
	maybe := !needs && score < 0.70

	return Decision{
		Quality:   score,
		NeedsOCR:  needs,
		MaybeOCR:  maybe,
		Reasons:   reasons,
		WordCount: wc,
	}
}

func normalize(s string) string {
	// Line ending normalization
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")

	// Remove excessive whitespace within lines, but preserve line breaks
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		// Collapse multiple spaces within a line
		fields := strings.Fields(line)
		lines[i] = strings.Join(fields, " ")
	}
	s = strings.Join(lines, "\n")

	// Collapse excessive newlines (keep max 2 for paragraph breaks)
	for strings.Contains(s, "\n\n\n\n") {
		s = strings.ReplaceAll(s, "\n\n\n\n", "\n\n")
	}

	return strings.TrimSpace(s)
}

func splitLines(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	raw := strings.Split(s, "\n")
	out := make([]string, 0, len(raw))
	for _, ln := range raw {
		ln = strings.TrimSpace(ln)
		if ln != "" {
			out = append(out, ln)
		}
	}
	return out
}

func lineStats(lines []string) (avg float64, shortRatio float64) {
	if len(lines) == 0 {
		return 0, 0
	}
	short := 0
	sum := 0
	for _, ln := range lines {
		l := len([]rune(ln))
		sum += l
		// Reduced threshold - books often have 30-50 char lines
		if l < 15 {
			short++
		}
	}
	avg = float64(sum) / float64(len(lines))
	shortRatio = float64(short) / float64(len(lines))
	return
}

func uniqueWordRatio(s string) float64 {
	ws := strings.Fields(strings.ToLower(s))
	if len(ws) == 0 {
		return 0
	}
	set := make(map[string]struct{}, len(ws))
	for _, w := range ws {
		set[w] = struct{}{}
	}
	return float64(len(set)) / float64(len(ws))
}

func hasRepeatedCharPatterns(s string) bool {
	// Detects patterns like "....." or "-----" (5+ repetitions)
	if len(s) < 5 {
		return false
	}

	consecutiveCount := 1
	var lastChar rune

	for _, char := range s {
		if char == lastChar {
			consecutiveCount++
			if consecutiveCount >= 5 {
				return true
			}
		} else {
			consecutiveCount = 1
			lastChar = char
		}
	}

	return false
}

func countScrambledRatio(s string) float64 {
	// Detects if text has many single-character "words"
	// which often indicates scrambled extraction
	words := strings.Fields(s)
	if len(words) == 0 {
		return 0
	}

	singleCharCount := 0
	for _, w := range words {
		if len([]rune(w)) == 1 {
			singleCharCount++
		}
	}

	return float64(singleCharCount) / float64(len(words))
}

func countIf(s string, pred func(rune) bool) int {
	n := 0
	for _, r := range s {
		if pred(r) {
			n++
		}
	}
	return n
}

func countGarbage(s string) int {
	n := 0
	for _, r := range s {
		// Unicode replacement char or control chars (excluding newline/tab)
		if r == '\uFFFD' || (unicode.IsControl(r) && r != '\n' && r != '\t') {
			n++
		}
	}
	return n
}

func safeDiv(a, b float64) float64 {
	if b <= 0 {
		return 0
	}
	return a / b
}

func clamp(x, lo, hi float64) float64 {
	return math.Max(lo, math.Min(hi, x))
}

// countBulletPointRatio detects structured content like lecture notes
func countBulletPointRatio(lines []string) float64 {
	if len(lines) == 0 {
		return 0
	}

	bulletCount := 0
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if len(trimmed) == 0 {
			continue
		}

		// Common bullet patterns
		firstChar := []rune(trimmed)[0]
		if firstChar == '•' || firstChar == '◦' || firstChar == '▪' || firstChar == '–' || firstChar == '-' {
			bulletCount++
			continue
		}

		// Numbered lists (1. 2. etc)
		if len(trimmed) > 2 && unicode.IsDigit(firstChar) && trimmed[1] == '.' {
			bulletCount++
			continue
		}

		// Letter lists (a. b. etc)
		if len(trimmed) > 2 && unicode.IsLetter(firstChar) && trimmed[1] == '.' {
			bulletCount++
		}
	}

	return float64(bulletCount) / float64(len(lines))
}

// detectEquationLikeContent detects math/code content
func detectEquationLikeContent(text string) bool {
	// Look for mathematical symbols and patterns
	mathSymbols := []string{
		"=", "≈", "≠", "±", "×", "÷", "∑", "∫", "∂", "√",
		"α", "β", "γ", "θ", "λ", "π", "σ", "Δ", "Ω",
		"∈", "∉", "⊂", "⊃", "∪", "∩", "∀", "∃",
	}

	count := 0
	for _, sym := range mathSymbols {
		if strings.Contains(text, sym) {
			count++
			if count >= 3 { // At least 3 different math symbols
				return true
			}
		}
	}

	// Look for equation-like patterns: "x = y", "f(x)", etc.
	// Simple heuristic: lots of equals signs relative to length
	equalsCount := strings.Count(text, "=")
	if len(text) > 100 && equalsCount > 5 {
		return true
	}

	// Look for code patterns: {}, [], ()
	braceCount := strings.Count(text, "{") + strings.Count(text, "[") + strings.Count(text, "(")
	if len(text) > 100 && braceCount > 10 {
		return true
	}

	return false
}
