package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func newWriteTool(t *testing.T) (*writeTool, string) {
	t.Helper()
	dir := t.TempDir()
	return &writeTool{cwd: dir, mq: newMutQueue()}, dir
}

func TestWriteCreatesParentDirs(t *testing.T) {
	w, dir := newWriteTool(t)
	res := execTool(t, w, `{"path":"a/b/c.txt","content":"hi"}`)
	if got := resultText(t, res); got != "Successfully wrote 2 bytes to a/b/c.txt" {
		t.Fatalf("got=%q", got)
	}
	data, err := os.ReadFile(filepath.Join(dir, "a/b/c.txt"))
	if err != nil || string(data) != "hi" {
		t.Fatalf("data=%q err=%v", data, err)
	}
}

func TestWriteOverwritesPreservingMode(t *testing.T) {
	w, dir := newWriteTool(t)
	path := filepath.Join(dir, "f.sh")
	if err := os.WriteFile(path, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	execTool(t, w, fmt.Sprintf(`{"path":%q,"content":"new"}`, path))
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("mode=%v, want 0755 preserved", info.Mode().Perm())
	}
	data, _ := os.ReadFile(path)
	if string(data) != "new" {
		t.Fatalf("data=%q", data)
	}
}

func TestWriteLeavesNoTempOnSuccess(t *testing.T) {
	w, dir := newWriteTool(t)
	execTool(t, w, `{"path":"f.txt","content":"x"}`)
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".mixi-tmp-") {
			t.Fatalf("leftover temp file %s", e.Name())
		}
	}
}

func TestWriteConcurrentSameFileLastWins(t *testing.T) {
	w, dir := newWriteTool(t)
	path := filepath.Join(dir, "race.txt")
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			content := strings.Repeat(fmt.Sprintf("%d", n%10), 2048)
			execTool(t, w, fmt.Sprintf(`{"path":%q,"content":%q}`, path, content))
		}(i)
	}
	wg.Wait()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Whatever write won, the file must be exactly one writer's content —
	// never interleaved bytes from two writers.
	if len(data) != 2048 || strings.Count(string(data), string(data[0])) != 2048 {
		t.Fatalf("torn write: len=%d", len(data))
	}
}
