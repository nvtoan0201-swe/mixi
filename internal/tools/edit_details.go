package tools

import (
	"encoding/json"
	"fmt"
	"strings"
)

// editDetails builds the UI-only metadata for an edit result: a unified-style
// patch (one hunk per edit, derived from the matched spans — no general diff
// algorithm needed since the changed ranges are known exactly), a display
// diff, and the first changed line number. Never sent to the LLM.
func editDetails(base string, edits []editSpec, spans []span) json.RawMessage {
	first := 0
	var patch strings.Builder
	// Walk edits in ascending span order so hunks appear top-to-bottom and
	// the running line drift from earlier edits is accounted for.
	order := make([]int, len(spans))
	for i := range order {
		order[i] = i
	}
	for i := 1; i < len(order); i++ {
		for j := i; j > 0 && spans[order[j]].start < spans[order[j-1]].start; j-- {
			order[j], order[j-1] = order[j-1], order[j]
		}
	}
	drift := 0
	for _, i := range order {
		oldStart := lineNumberAt(base, spans[i].start)
		// The matched text may start/end mid-line; hunk granularity is
		// good enough for UI preview.
		oldLines := strings.Split(base[spans[i].start:spans[i].end], "\n")
		newLines := strings.Split(edits[i].NewText, "\n")
		if first == 0 {
			first = oldStart
		}
		fmt.Fprintf(&patch, "@@ -%d,%d +%d,%d @@\n", oldStart, len(oldLines), oldStart+drift, len(newLines))
		for _, l := range oldLines {
			patch.WriteString("-" + l + "\n")
		}
		for _, l := range newLines {
			patch.WriteString("+" + l + "\n")
		}
		drift += len(newLines) - len(oldLines)
	}
	d, _ := json.Marshal(map[string]any{
		"diff":             patch.String(),
		"patch":            patch.String(),
		"firstChangedLine": first,
	})
	return d
}

// lineNumberAt returns the 1-indexed line containing byte offset off.
func lineNumberAt(s string, off int) int {
	return strings.Count(s[:off], "\n") + 1
}
