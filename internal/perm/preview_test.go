package perm

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/user/mixi-agent/internal/ai"
	"github.com/user/mixi-agent/internal/tools"
)

func previewEngine(t *testing.T, cwd string) *Engine {
	t.Helper()
	return newTestEngine(t, PolicyConfig{Mode: "prompt"}, &scriptedAsker{}, cwd)
}

// TestEditPreviewMatchesExecution is the consistency guarantee: the diff
// shown at approval time equals the diff the approved edit actually makes.
func TestEditPreviewMatchesExecution(t *testing.T) {
	cwd := t.TempDir()
	path := filepath.Join(cwd, "main.go")
	before := "package main\n\nfunc main() {\n\tprintln(\"hi\")\n}\n"
	if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}

	args, _ := json.Marshal(map[string]any{
		"path": "main.go",
		"edits": []map[string]string{
			{"oldText": "println(\"hi\")", "newText": "println(\"bye\")"},
		},
	})
	call := ai.ToolCall{ID: "t1", Name: "edit", Args: args}
	e := previewEngine(t, cwd)
	preview := e.preview(call, describeCall(call, cwd))

	// Execute the real edit tool with the same args.
	reg := tools.NewRegistry()
	jobs, err := tools.RegisterBuiltins(reg, tools.Options{Cwd: cwd})
	if err != nil {
		t.Fatal(err)
	}
	defer jobs.KillAll()
	edit, _ := reg.Get("edit")
	if res, err := edit.Execute(context.Background(), args, nil); err != nil || res.IsError {
		t.Fatalf("edit execution failed: %v %+v", err, res)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	wantDiff := UnifiedDiff("main.go", before, string(after))
	if preview != wantDiff {
		t.Fatalf("preview diff != post-execution diff\npreview:\n%s\nactual:\n%s", preview, wantDiff)
	}
	if !strings.Contains(preview, "+\tprintln(\"bye\")") {
		t.Fatalf("preview missing the change:\n%s", preview)
	}
}

func TestWritePreviewNewFile(t *testing.T) {
	cwd := t.TempDir()
	args, _ := json.Marshal(map[string]any{"path": "fresh.txt", "content": "hello"})
	call := ai.ToolCall{ID: "t1", Name: "write", Args: args}
	e := previewEngine(t, cwd)
	got := e.preview(call, describeCall(call, cwd))
	if got != "new file fresh.txt, 5 bytes" {
		t.Fatalf("new-file preview = %q", got)
	}
}

func TestWritePreviewExistingFileDiffs(t *testing.T) {
	cwd := t.TempDir()
	path := filepath.Join(cwd, "note.txt")
	if err := os.WriteFile(path, []byte("old line\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]any{"path": "note.txt", "content": "new line\n"})
	call := ai.ToolCall{ID: "t1", Name: "write", Args: args}
	e := previewEngine(t, cwd)
	got := e.preview(call, describeCall(call, cwd))
	if !strings.Contains(got, "-old line") || !strings.Contains(got, "+new line") {
		t.Fatalf("overwrite preview missing diff:\n%s", got)
	}
}

func TestBashPreviewShowsCommandAndCwd(t *testing.T) {
	cwd := t.TempDir()
	args, _ := json.Marshal(map[string]any{"command": "go test ./..."})
	call := ai.ToolCall{ID: "t1", Name: "bash", Args: args}
	e := previewEngine(t, cwd)
	got := e.preview(call, describeCall(call, cwd))
	if !strings.Contains(got, "$ go test ./...") || !strings.Contains(got, cwd) {
		t.Fatalf("bash preview = %q", got)
	}
}

func TestMCPPreviewPrettyPrintsArgs(t *testing.T) {
	cwd := t.TempDir()
	args := json.RawMessage(`{"repo":"x","labels":["a","b"]}`)
	call := ai.ToolCall{ID: "t1", Name: "mcp__github__create_issue", Args: args}
	e := previewEngine(t, cwd)
	got := e.preview(call, describeCall(call, cwd))
	if !strings.Contains(got, "\"repo\": \"x\"") {
		t.Fatalf("mcp preview not pretty-printed: %q", got)
	}
}
