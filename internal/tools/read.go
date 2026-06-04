package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/user/mixi-agent/internal/ai"
)

type readTool struct{ cwd string }

func (t *readTool) Name() string { return "read" }

func (t *readTool) Description() string {
	return "Read a file from the filesystem. Supports text and images (png/jpeg/gif/webp). " +
		"Returns at most 2000 lines or 50KB; use offset/limit to page."
}

func (t *readTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{
"path":{"type":"string","description":"File path, absolute or relative to cwd"},
"offset":{"type":"integer","minimum":1,"description":"1-indexed first line to read"},
"limit":{"type":"integer","minimum":1,"description":"Max lines to return"}
},"required":["path"]}`)
}

func (t *readTool) Mode() ExecMode { return ExecParallel }

func (t *readTool) Execute(ctx context.Context, args json.RawMessage, _ chan<- ToolUpdate) (ToolResult, error) {
	var a struct {
		Path   string `json:"path"`
		Offset int    `json:"offset"`
		Limit  int    `json:"limit"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return ToolResult{}, fmt.Errorf("read: bad arguments: %w", err)
	}
	path := resolvePath(t.cwd, a.Path)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Errorf("read %s: no such file", a.Path), nil
	}
	if err != nil {
		return ToolResult{}, fmt.Errorf("read %s: %w", a.Path, err)
	}
	if mime := sniffImageMime(data); mime != "" {
		return readImage(a.Path, data, mime)
	}
	if bytes.IndexByte(data[:min(len(data), 8192)], 0) >= 0 {
		return Errorf("read %s: binary file; use bash xxd/strings", a.Path), nil
	}
	return readText(a.Path, string(data), a.Offset, a.Limit)
}

// readText pages 1-indexed lines and head-truncates with continuation hints.
func readText(path, content string, offset, limit int) (ToolResult, error) {
	if content == "" {
		return Text("(empty file)"), nil
	}
	lines := strings.Split(content, "\n")
	// A trailing newline yields a phantom empty last element; drop it so
	// line counts match what editors report.
	if n := len(lines); n > 1 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	total := len(lines)
	if offset == 0 {
		offset = 1
	}
	if offset > total {
		return Errorf("Offset %d is beyond end of file (%d lines total)", offset, total), nil
	}
	window := lines[offset-1:]
	if limit > 0 && limit < len(window) {
		window = window[:limit]
	}
	kept, _ := headTruncate(window, MaxLines, MaxBytes)
	if len(kept) == 0 {
		return Errorf("read %s: line %d is %d bytes (max %d); use bash: sed -n '%dp' %s | head -c %d",
			path, offset, len(window[0]), MaxBytes, offset, path, MaxBytes), nil
	}

	var b strings.Builder
	for i, line := range kept {
		fmt.Fprintf(&b, "%d→%s\n", offset+i, line)
	}
	lastShown := offset + len(kept) - 1
	if lastShown < total {
		fmt.Fprintf(&b, "[Showing lines %d–%d of %d. Use offset=%d to continue.]",
			offset, lastShown, total, lastShown+1)
	}
	return Text(strings.TrimSuffix(b.String(), "\n")), nil
}

// readImage emits ImageContent, downscaling anything over 2000×2000.
func readImage(path string, data []byte, mime string) (ToolResult, error) {
	out, outMime, err := resizeImageIfNeeded(data, mime)
	if err != nil {
		return Errorf("read %s: cannot decode %s image: %v", path, mime, err), nil
	}
	return ToolResult{Content: []ai.Content{ai.ImageContent{
		Data:     base64Encode(out),
		MimeType: outMime,
	}}}, nil
}
