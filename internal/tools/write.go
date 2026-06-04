package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

type writeTool struct {
	cwd   string
	fsync bool
	mq    *mutQueue
}

func (t *writeTool) Name() string { return "write" }

func (t *writeTool) Description() string {
	return "Write content to a file, creating parent directories. Overwrites existing files."
}

func (t *writeTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{
"path":{"type":"string","description":"File path, absolute or relative to cwd"},
"content":{"type":"string","description":"Full file content to write"}
},"required":["path","content"]}`)
}

func (t *writeTool) Mode() ExecMode { return ExecParallel }

func (t *writeTool) Execute(ctx context.Context, args json.RawMessage, _ chan<- ToolUpdate) (ToolResult, error) {
	var a struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return ToolResult{}, fmt.Errorf("write: bad arguments: %w", err)
	}
	path := resolvePath(t.cwd, a.Path)

	unlock := t.mq.lock(path)
	defer unlock()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return ToolResult{}, fmt.Errorf("write %s: %w", a.Path, err)
	}
	if err := atomicWrite(path, []byte(a.Content), t.fsync); err != nil {
		return ToolResult{}, fmt.Errorf("write %s: %w", a.Path, err)
	}
	return Text(fmt.Sprintf("Successfully wrote %d bytes to %s", len(a.Content), a.Path)), nil
}

// atomicWrite lands content via temp file + rename in the target directory
// so a crash never leaves a torn file. Preserves an existing file's mode.
func atomicWrite(path string, content []byte, fsync bool) error {
	mode := fs.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".mixi-tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op after successful rename

	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		return err
	}
	if fsync {
		if err := tmp.Sync(); err != nil {
			tmp.Close()
			return err
		}
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
