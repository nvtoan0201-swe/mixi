package tools

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func findFixtureDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, f := range []string{
		"main.go", "util.go", "readme.md",
		"src/app.ts", "src/deep/widget.ts",
		".hidden/secret.go",
		".git/objects/aa", // must be skipped by the fallback
	} {
		p := filepath.Join(dir, f)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// forceWalkFallback hides fd so the pure-Go path runs.
func forceWalkFallback(t *testing.T) {
	withFakeLookPath(t, func(string) (string, error) {
		return "", errors.New("not found")
	})
}

func TestFindFallbackBasenameGlob(t *testing.T) {
	forceWalkFallback(t)
	f := &findTool{cwd: findFixtureDir(t)}
	out := resultText(t, execTool(t, f, `{"pattern":"*.go"}`))
	for _, want := range []string{"main.go", "util.go", "secret.go"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in %q", want, out)
		}
	}
	if strings.Contains(out, "app.ts") || strings.Contains(out, ".git/") {
		t.Fatalf("unexpected entries: %q", out)
	}
	if !strings.Contains(out, "[fd not found: .gitignore not respected]") {
		t.Fatalf("missing fallback note: %q", out)
	}
}

func TestFindFallbackFullPathGlob(t *testing.T) {
	forceWalkFallback(t)
	f := &findTool{cwd: findFixtureDir(t)}
	out := resultText(t, execTool(t, f, `{"pattern":"src/**/*.ts"}`))
	if !strings.Contains(out, filepath.Join("src", "app.ts")) ||
		!strings.Contains(out, filepath.Join("src", "deep", "widget.ts")) {
		t.Fatalf("out=%q", out)
	}
}

func TestFindFallbackLimitTruncates(t *testing.T) {
	forceWalkFallback(t)
	f := &findTool{cwd: findFixtureDir(t)}
	out := resultText(t, execTool(t, f, `{"pattern":"*","limit":2}`))
	if !strings.Contains(out, "[truncated at 2 results]") {
		t.Fatalf("out=%q", out)
	}
}

func TestFindFallbackNoMatches(t *testing.T) {
	forceWalkFallback(t)
	f := &findTool{cwd: findFixtureDir(t)}
	out := resultText(t, execTool(t, f, `{"pattern":"*.rs"}`))
	if !strings.HasPrefix(out, "no files matched") {
		t.Fatalf("out=%q", out)
	}
}

func TestFindFallbackPathArgPrefixesResults(t *testing.T) {
	forceWalkFallback(t)
	f := &findTool{cwd: findFixtureDir(t)}
	out := resultText(t, execTool(t, f, `{"pattern":"*.ts","path":"src"}`))
	if !strings.Contains(out, filepath.Join("src", "app.ts")) {
		t.Fatalf("out=%q", out)
	}
}

func TestFindWithRealFd(t *testing.T) {
	if _, ok := findBinary("fd", "fdfind"); !ok {
		t.Skip("fd not installed in this environment")
	}
	f := &findTool{cwd: findFixtureDir(t)}
	out := resultText(t, execTool(t, f, `{"pattern":"*.go"}`))
	if !strings.Contains(out, "main.go") {
		t.Fatalf("out=%q", out)
	}
	if strings.Contains(out, "[fd not found") {
		t.Fatalf("fd path must not carry the fallback note: %q", out)
	}
}
