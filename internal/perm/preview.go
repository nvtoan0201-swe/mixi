package perm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/user/mixi-agent/internal/ai"
	"github.com/user/mixi-agent/internal/tools"
)

// preview renders what a call would do, for the approval prompt: a dry-run
// diff for file mutations, the command for bash, pretty args otherwise.
func (e *Engine) preview(call ai.ToolCall, info CallInfo) string {
	switch call.Name {
	case "edit":
		path, before, after, err := tools.DryRunEdit(e.cwd, call.Args)
		if err != nil {
			return fmt.Sprintf("edit %s: %v", e.displayPath(path), err)
		}
		return UnifiedDiff(e.displayPath(path), before, after)
	case "write":
		return e.writePreview(call.Args, info.Path)
	case "bash":
		return fmt.Sprintf("$ %s\ncwd: %s", info.Command, e.cwd)
	default:
		return prettyArgs(call.Args)
	}
}

// writePreview diffs the proposed content against the existing file, or
// reports the size of a brand-new one.
func (e *Engine) writePreview(args json.RawMessage, absPath string) string {
	var a struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return fmt.Sprintf("write: bad arguments: %v", err)
	}
	name := e.displayPath(absPath)
	old, err := os.ReadFile(absPath)
	if err != nil {
		return fmt.Sprintf("new file %s, %d bytes", name, len(a.Content))
	}
	return UnifiedDiff(name, string(old), a.Content)
}

// displayPath shortens an absolute path to cwd-relative when it fits.
func (e *Engine) displayPath(absPath string) string {
	if rel, err := filepath.Rel(e.cwd, absPath); err == nil && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return absPath
}

// prettyArgs indents the raw args JSON; raw fallback if it isn't valid JSON.
func prettyArgs(args json.RawMessage) string {
	var out bytes.Buffer
	if err := json.Indent(&out, args, "", "  "); err != nil {
		return string(args)
	}
	return out.String()
}
