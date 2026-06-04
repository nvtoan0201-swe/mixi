package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLsSortsAndMarksDirs(t *testing.T) {
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, "Zdir"), 0o755)
	os.WriteFile(filepath.Join(dir, "apple.txt"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(dir, ".dotfile"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(dir, "Banana.txt"), []byte("x"), 0o644)

	l := &lsTool{cwd: dir}
	out := resultText(t, execTool(t, l, `{}`))
	// Case-insensitive order, dotfiles included, dirs suffixed.
	want := ".dotfile\napple.txt\nBanana.txt\nZdir/"
	if out != want {
		t.Fatalf("out=%q want=%q", out, want)
	}
}

func TestLsLimitTruncates(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 10; i++ {
		os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%02d.txt", i)), []byte("x"), 0o644)
	}
	l := &lsTool{cwd: dir}
	out := resultText(t, execTool(t, l, `{"limit":4}`))
	if !strings.Contains(out, "[truncated at 4 entries]") {
		t.Fatalf("out=%q", out)
	}
	if strings.Count(out, ".txt") != 4 {
		t.Fatalf("want 4 entries, out=%q", out)
	}
}

func TestLsMissingDirectory(t *testing.T) {
	l := &lsTool{cwd: t.TempDir()}
	res := execTool(t, l, `{"path":"ghost"}`)
	if !res.IsError || !strings.Contains(resultText(t, res), "no such directory") {
		t.Fatalf("res=%q", resultText(t, res))
	}
}

func TestLsEmptyDirectory(t *testing.T) {
	l := &lsTool{cwd: t.TempDir()}
	if got := resultText(t, execTool(t, l, `{}`)); got != "(empty directory)" {
		t.Fatalf("got=%q", got)
	}
}
