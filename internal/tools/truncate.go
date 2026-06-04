package tools

import (
	"strings"
	"time"
	"unicode/utf8"
)

// Output limits shared by all built-in tools (Pi-compatible values).
const (
	// MaxLines is the most lines any tool returns in one result.
	MaxLines = 2000
	// MaxBytes is the most content bytes any tool returns in one result.
	MaxBytes = 50 * 1024
	// GrepMaxLineLen clips individual grep match lines.
	GrepMaxLineLen = 500
	// BashUpdateThrottle is the minimum interval between streamed
	// partial-output updates from bash.
	BashUpdateThrottle = 100 * time.Millisecond
	// BashDefaultTimeout applies when the model omits a bash timeout;
	// BashMaxTimeout caps what it may request.
	BashDefaultTimeout = 120 * time.Second
	BashMaxTimeout     = 600 * time.Second
)

// headTruncate keeps leading lines until the first of maxLines lines or
// maxBytes bytes is hit. Returns the kept lines and whether anything was cut.
// A line that would individually overflow maxBytes is not split: if it is the
// first line nothing is kept (caller reports the oversized-line error).
func headTruncate(lines []string, maxLines, maxBytes int) (kept []string, truncated bool) {
	bytes := 0
	for i, line := range lines {
		if i >= maxLines {
			return lines[:i], true
		}
		bytes += len(line) + 1 // count the newline
		if bytes > maxBytes {
			return lines[:i], true
		}
	}
	return lines, false
}

// tailTruncate keeps the trailing maxLines lines / maxBytes bytes of s,
// cutting on a line boundary where possible and never splitting a UTF-8
// sequence. Used by bash, which (unlike read) shows the END of output.
func tailTruncate(s string, maxLines, maxBytes int) (out string, truncated bool) {
	if len(s) > maxBytes {
		s = cutUTF8Start(s[len(s)-maxBytes:])
		// Drop the likely-partial first line so output starts cleanly.
		if i := strings.IndexByte(s, '\n'); i >= 0 && i+1 < len(s) {
			s = s[i+1:]
		}
		truncated = true
	}
	lines := strings.Split(s, "\n")
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
		s = strings.Join(lines, "\n")
		truncated = true
	}
	return s, truncated
}

// cutUTF8Start trims leading continuation bytes left by a byte-offset cut so
// the string starts on a rune boundary.
func cutUTF8Start(s string) string {
	for i := 0; i < len(s) && i < utf8.UTFMax; i++ {
		if utf8.RuneStart(s[i]) {
			return s[i:]
		}
	}
	return s
}

// clipLine shortens a single line to max bytes on a rune boundary, appending
// an ellipsis marker when clipped.
func clipLine(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}
