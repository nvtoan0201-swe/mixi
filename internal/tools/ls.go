package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

type lsTool struct{ cwd string }

func (t *lsTool) Name() string { return "ls" }

func (t *lsTool) Description() string {
	return "List directory entries. Directories are suffixed with /; dotfiles included."
}

func (t *lsTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{
"path":{"type":"string","default":".","description":"Directory to list; defaults to cwd"},
"limit":{"type":"integer","minimum":1,"default":500,"description":"Max entries"}
},"required":[]}`)
}

func (t *lsTool) Mode() ExecMode { return ExecParallel }

func (t *lsTool) Execute(ctx context.Context, args json.RawMessage, _ chan<- ToolUpdate) (ToolResult, error) {
	var a struct {
		Path  string `json:"path"`
		Limit int    `json:"limit"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return ToolResult{}, fmt.Errorf("ls: bad arguments: %w", err)
	}
	if a.Path == "" {
		a.Path = "."
	}
	if a.Limit <= 0 {
		a.Limit = 500
	}
	dir := resolvePath(t.cwd, a.Path)
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return Errorf("ls %s: no such directory", a.Path), nil
	}
	if err != nil {
		return ToolResult{}, fmt.Errorf("ls %s: %w", a.Path, err)
	}
	if len(entries) == 0 {
		return Text("(empty directory)"), nil
	}

	sort.Slice(entries, func(i, j int) bool {
		return strings.ToLower(entries[i].Name()) < strings.ToLower(entries[j].Name())
	})
	truncated := len(entries) > a.Limit
	if truncated {
		entries = entries[:a.Limit]
	}
	var b strings.Builder
	for _, e := range entries {
		b.WriteString(e.Name())
		if e.IsDir() {
			b.WriteByte('/')
		}
		b.WriteByte('\n')
	}
	out := strings.TrimSuffix(b.String(), "\n")
	if truncated {
		out += fmt.Sprintf("\n[truncated at %d entries]", a.Limit)
	}
	return Text(out), nil
}
