package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

type editTool struct {
	cwd string
	mq  *mutQueue
	obs FileObserver
}

func (t *editTool) Name() string { return "edit" }

func (t *editTool) Description() string {
	return "Apply one or more exact string replacements to a file. Each oldText must match exactly once. " +
		"If an oldText only matches after typographic normalization (smart quotes, unicode dashes, " +
		"trailing whitespace), the whole file is rewritten in normalized form."
}

func (t *editTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{
"path":{"type":"string","description":"File path, absolute or relative to cwd"},
"edits":{"type":"array","minItems":1,"items":{"type":"object","properties":{
"oldText":{"type":"string","minLength":1,"description":"Exact text to replace; must be unique in the file"},
"newText":{"type":"string","description":"Replacement text"}
},"required":["oldText","newText"]}}
},"required":["path","edits"]}`)
}

func (t *editTool) Mode() ExecMode { return ExecParallel }

func (t *editTool) Execute(ctx context.Context, args json.RawMessage, _ chan<- ToolUpdate) (ToolResult, error) {
	var a struct {
		Path  string     `json:"path"`
		Edits []editSpec `json:"edits"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return ToolResult{}, fmt.Errorf("edit: bad arguments: %w", err)
	}
	for i, e := range a.Edits {
		if e.OldText == "" {
			return Errorf("edits[%d].oldText is empty", i), nil
		}
	}
	path := resolvePath(t.cwd, a.Path)

	unlock := t.mq.lock(path)
	defer unlock()

	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Errorf("edit %s: no such file", a.Path), nil
	}
	if err != nil {
		return ToolResult{}, fmt.Errorf("edit %s: %w", a.Path, err)
	}

	content, hadBOM, hadCRLF := stripLineFormat(string(raw))

	base, spans, _, err := matchEdits(content, a.Path, a.Edits)
	if err != nil {
		return Errorf("%s", err.Error()), nil
	}
	if err := validateOverlaps(spans); err != nil {
		return Errorf("%s", err.Error()), nil
	}
	updated := applyEdits(base, a.Edits, spans)

	details := editDetails(base, a.Edits, spans)
	out := restoreLineFormat(updated, hadBOM, hadCRLF)
	if err := atomicWrite(path, []byte(out), false); err != nil {
		return ToolResult{}, fmt.Errorf("edit %s: %w", a.Path, err)
	}
	observeWrite(t.obs, path, []byte(out))

	res := Text(fmt.Sprintf("Applied %d edit(s) to %s", len(a.Edits), a.Path))
	res.Details = details
	return res, nil
}

// bomMark is the UTF-8 byte order mark as a string.
const bomMark = "\uFEFF"

// stripLineFormat removes a UTF-8 BOM and normalizes CRLF→LF for matching,
// reporting what was present so the write path can restore it byte-exact.
func stripLineFormat(s string) (content string, hadBOM, hadCRLF bool) {
	if strings.HasPrefix(s, bomMark) {
		hadBOM = true
		s = strings.TrimPrefix(s, bomMark)
	}
	if strings.Contains(s, "\r\n") {
		hadCRLF = true
		s = strings.ReplaceAll(s, "\r\n", "\n")
	}
	return s, hadBOM, hadCRLF
}

func restoreLineFormat(s string, hadBOM, hadCRLF bool) string {
	if hadCRLF {
		s = strings.ReplaceAll(s, "\n", "\r\n")
	}
	if hadBOM {
		s = bomMark + s
	}
	return s
}
