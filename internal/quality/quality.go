package quality

import (
	"math"
	"regexp"
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

	// Basic features
	total := float64(len([]rune(clean)))
	alpha := float64(countIf(clean, func(r rune) bool { return unicode.IsLetter(r) }))
	digits := float64(countIf(clean, func(r rune) bool { return unicode.IsDigit(r) }))
	garbage := float64(countGarbage(clean))

	alphaRatio := safeDiv(alpha, total)
	digitRatio := safeDiv(digits, total)
	garbageRatio := safeDiv(garbage, total)

	lines := splitLines(clean)
	lineCount := len(lines)
	avgLineLen, shortLineRatio := lineStats(lines)

	uniqueWordRatio := uniqueWordRatio(clean)

	// Score starts at 1 and gets penalized
	score := 1.0
	reasons := []string{}

	// Word count is still useful, just not the only signal
	if wc < minWords {
		score -= 0.45
		reasons = append(reasons, "low_word_count")
	}

	if alphaRatio < 0.35 {
		score -= 0.35
		reasons = append(reasons, "low_alpha_ratio")
	}

	// Lots of replacement chars, control chars, etc.
	if garbageRatio > 0.01 {
		score -= 0.40
		reasons = append(reasons, "garbage_chars")
	}

	// Fragmented extraction looks like many short lines / columns getting shredded
	if lineCount > 0 && shortLineRatio > 0.55 && avgLineLen < 25 {
		score -= 0.25
		reasons = append(reasons, "fragmented_lines")
	}

	// Repetitive junk (headers repeated, OCR artifacts) can show up as low uniqueness
	if wc > 30 && uniqueWordRatio < 0.25 {
		score -= 0.15
		reasons = append(reasons, "low_unique_words")
	}

	// Edge: digits-only tables might still be meaningful. Reduce penalty a bit.
	if digitRatio > 0.25 && alphaRatio < 0.2 && wc >= minWords {
		score += 0.10
		reasons = append(reasons, "numeric_heavy")
	}

	score = clamp(score, 0, 1)

	// Decision bands
	needs := score < 0.55
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
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = regexp.MustCompile(`[ \t]+`).ReplaceAllString(s, " ")
	s = regexp.MustCompile(`\n{4,}`).ReplaceAllString(s, "\n\n")
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
		if l < 20 {
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
	set := map[string]struct{}{}
	for _, w := range ws {
		set[w] = struct{}{}
	}
	return float64(len(set)) / float64(len(ws))
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
		// replacement char or control (excluding newline/tab already normalized)
		if r == '\uFFFD' || (unicode.IsControl(r) && r != '\n') {
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
