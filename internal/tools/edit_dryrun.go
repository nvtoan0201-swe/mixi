package tools

import (
	"encoding/json"
	"fmt"
	"os"
)

// DryRunEdit runs the edit tool's full match pipeline on the current file
// content without writing, returning the before/after states. Used to build
// permission previews that are guaranteed to equal what an approved edit
// will actually do (same matching, same normalization).
func DryRunEdit(cwd string, args json.RawMessage) (path, before, after string, err error) {
	var a struct {
		Path  string     `json:"path"`
		Edits []editSpec `json:"edits"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return "", "", "", fmt.Errorf("edit: bad arguments: %w", err)
	}
	path = resolvePath(cwd, a.Path)
	raw, err := os.ReadFile(path)
	if err != nil {
		return path, "", "", err
	}
	content, hadBOM, hadCRLF := stripLineFormat(string(raw))
	base, spans, _, err := matchEdits(content, a.Path, a.Edits)
	if err != nil {
		return path, "", "", err
	}
	if err := validateOverlaps(spans); err != nil {
		return path, "", "", err
	}
	updated := applyEdits(base, a.Edits, spans)
	return path, restoreLineFormat(base, hadBOM, hadCRLF), restoreLineFormat(updated, hadBOM, hadCRLF), nil
}
