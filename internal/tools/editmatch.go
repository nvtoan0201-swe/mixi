package tools

import (
	"fmt"
	"strings"

	"golang.org/x/text/unicode/norm"
)

// charNormTable maps typographic characters LLMs commonly emit to the ASCII
// the file almost certainly contains. Applied after NFKC (which already
// folds NBSP/thin spaces and fullwidth forms but leaves quotes and dashes).
var charNormTable = map[rune]rune{
	'‘': '\'', // left single quote
	'’': '\'', // right single quote
	'‚': '\'', // single low-9 quote
	'‛': '\'', // single high-reversed-9 quote
	'“': '"',  // left double quote
	'”': '"',  // right double quote
	'„': '"',  // double low-9 quote
	'‟': '"',  // double high-reversed-9 quote
	'‐': '-',  // hyphen
	'‑': '-',  // non-breaking hyphen
	'‒': '-',  // figure dash
	'–': '-',  // en dash
	'—': '-',  // em dash
	'―': '-',  // horizontal bar
	' ': ' ',  // NBSP (also folded by NFKC; kept for clarity)
	' ': ' ',  // figure space
	' ': ' ',  // narrow NBSP
}

// normalizeForMatch makes content and needle comparable despite LLM
// typography drift: NFKC, char table above, trailing whitespace stripped per
// line. When fuzzy matching is used the file is REWRITTEN from this form —
// that surprising-but-deliberate Pi semantic is stated in the tool description.
func normalizeForMatch(s string) string {
	s = norm.NFKC.String(s)
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if mapped, ok := charNormTable[r]; ok {
			r = mapped
		}
		b.WriteRune(r)
	}
	lines := strings.Split(b.String(), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t")
	}
	return strings.Join(lines, "\n")
}

// editSpec is one oldText→newText replacement request.
type editSpec struct {
	OldText string `json:"oldText"`
	NewText string `json:"newText"`
}

// span is a half-open byte range [start,end) in the match base.
type span struct{ start, end int }

// matchEdits locates every edit in content. All edits match exactly → base
// is content untouched. If ANY edit needs the fuzzy pass, base becomes the
// normalized content and every edit is re-matched there (whole-file
// normalized rewrite, Pi contract). Spans are returned in input order.
func matchEdits(content, path string, edits []editSpec) (base string, spans []span, usedFuzzy bool, err error) {
	spans = make([]span, len(edits))
	exact := true
	for i, e := range edits {
		switch strings.Count(content, e.OldText) {
		case 1:
			off := strings.Index(content, e.OldText)
			spans[i] = span{off, off + len(e.OldText)}
		case 0:
			exact = false
		default:
			return "", nil, false, fmt.Errorf(
				"Found %d occurrences. The text must be unique — include more surrounding context.",
				strings.Count(content, e.OldText))
		}
	}
	if exact {
		return content, spans, false, nil
	}

	base = normalizeForMatch(content)
	for i, e := range edits {
		needle := normalizeForMatch(e.OldText)
		switch n := strings.Count(base, needle); n {
		case 1:
			off := strings.Index(base, needle)
			spans[i] = span{off, off + len(needle)}
		case 0:
			return "", nil, false, fmt.Errorf("oldText not found in %s%s", path, closestLineHint(base, needle))
		default:
			return "", nil, false, fmt.Errorf(
				"Found %d occurrences. The text must be unique — include more surrounding context.", n)
		}
	}
	return base, spans, true, nil
}

// validateOverlaps rejects edits whose ranges intersect; the model must
// merge them into one edit instead.
func validateOverlaps(spans []span) error {
	for i := range spans {
		for j := i + 1; j < len(spans); j++ {
			if spans[i].start < spans[j].end && spans[j].start < spans[i].end {
				return fmt.Errorf("edits[%d] and edits[%d] overlap; merge them", i, j)
			}
		}
	}
	return nil
}

// applyEdits replaces all spans in base, working in reverse offset order so
// earlier spans stay valid. Spans were matched against base once, up front.
func applyEdits(base string, edits []editSpec, spans []span) string {
	order := make([]int, len(spans))
	for i := range order {
		order[i] = i
	}
	// Sort descending by start (insertion sort — edit lists are tiny).
	for i := 1; i < len(order); i++ {
		for j := i; j > 0 && spans[order[j]].start > spans[order[j-1]].start; j-- {
			order[j], order[j-1] = order[j-1], order[j]
		}
	}
	out := base
	for _, i := range order {
		out = out[:spans[i].start] + edits[i].NewText + out[spans[i].end:]
	}
	return out
}

// closestLineHint suggests the most similar file line to the needle's first
// line, helping the model fix near-miss oldText quickly.
func closestLineHint(content, needle string) string {
	target := strings.TrimSpace(strings.SplitN(needle, "\n", 2)[0])
	if target == "" {
		return ""
	}
	bestScore, bestLine, bestNum := 0.0, "", 0
	for i, line := range strings.Split(content, "\n") {
		score := bigramSimilarity(target, strings.TrimSpace(line))
		if score > bestScore {
			bestScore, bestLine, bestNum = score, strings.TrimSpace(line), i+1
		}
	}
	if bestScore < 0.3 {
		return ""
	}
	return fmt.Sprintf(" (closest match is line %d: %q)", bestNum, clipLine(bestLine, 120))
}

// bigramSimilarity is the Sørensen–Dice coefficient over byte bigrams —
// cheap and good enough to rank near-miss lines.
func bigramSimilarity(a, b string) float64 {
	if a == b {
		return 1
	}
	if len(a) < 2 || len(b) < 2 {
		return 0
	}
	grams := map[string]int{}
	for i := 0; i+2 <= len(a); i++ {
		grams[a[i:i+2]]++
	}
	overlap := 0
	for i := 0; i+2 <= len(b); i++ {
		if grams[b[i:i+2]] > 0 {
			grams[b[i:i+2]]--
			overlap++
		}
	}
	return 2 * float64(overlap) / float64(len(a)+len(b)-2)
}
