package tools

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withFakeLookPath swaps the binary resolver for the duration of the test.
func withFakeLookPath(t *testing.T, fn func(string) (string, error)) {
	t.Helper()
	old := lookPath
	lookPath = fn
	t.Cleanup(func() { lookPath = old })
}

func TestGrepMissingRipgrepInstallHint(t *testing.T) {
	withFakeLookPath(t, func(string) (string, error) {
		return "", errors.New("not found")
	})
	g := &grepTool{cwd: t.TempDir()}
	res := execTool(t, g, `{"pattern":"x"}`)
	out := resultText(t, res)
	if !res.IsError || !strings.Contains(out, "ripgrep (rg) not found on PATH") || !strings.Contains(out, "Install it:") {
		t.Fatalf("out=%q", out)
	}
}

func grepFixtureDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"a.go":       "package main\nfunc TargetFunc() {}\n",
		"b.txt":      "no match here\n",
		"sub/c.go":   "// TargetFunc caller\nlong line: " + strings.Repeat("z", 600) + " TargetFunc\n",
		"unique.txt": "needle-alpha\nneedle-beta\nneedle-gamma\n",
	}
	for name, content := range files {
		p := filepath.Join(dir, name)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func requireRg(t *testing.T) {
	t.Helper()
	if _, ok := findBinary("rg"); !ok {
		t.Skip("ripgrep not installed in this environment")
	}
}

func TestGrepFindsMatchesWithFileLine(t *testing.T) {
	requireRg(t)
	g := &grepTool{cwd: grepFixtureDir(t)}
	out := resultText(t, execTool(t, g, `{"pattern":"TargetFunc"}`))
	if !strings.Contains(out, "a.go:2:") || !strings.Contains(out, "c.go:1:") {
		t.Fatalf("out=%q", out)
	}
}

func TestGrepNoMatches(t *testing.T) {
	requireRg(t)
	g := &grepTool{cwd: grepFixtureDir(t)}
	if got := resultText(t, execTool(t, g, `{"pattern":"zzz-not-there"}`)); got != "no matches found" {
		t.Fatalf("got=%q", got)
	}
}

func TestGrepClipsLongLines(t *testing.T) {
	requireRg(t)
	g := &grepTool{cwd: grepFixtureDir(t)}
	out := resultText(t, execTool(t, g, `{"pattern":"long line"}`))
	for _, line := range strings.Split(out, "\n") {
		if len(line) > GrepMaxLineLen+100 { // path prefix + clipped content
			t.Fatalf("line not clipped: %d bytes", len(line))
		}
	}
}

func TestGrepLimitTruncates(t *testing.T) {
	requireRg(t)
	g := &grepTool{cwd: grepFixtureDir(t)}
	out := resultText(t, execTool(t, g, `{"pattern":"needle-","limit":2}`))
	if !strings.Contains(out, "[truncated at 2 matches]") {
		t.Fatalf("out=%q", out)
	}
	if strings.Count(out, "needle-") != 2 {
		t.Fatalf("want exactly 2 matches, out=%q", out)
	}
}

func TestGrepLiteralAndIgnoreCase(t *testing.T) {
	requireRg(t)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "f.txt"), []byte("a.b(c)\nUPPER text\n"), 0o644)
	g := &grepTool{cwd: dir}
	if out := resultText(t, execTool(t, g, `{"pattern":"a.b(c)","literal":true}`)); !strings.Contains(out, "f.txt:1") {
		t.Fatalf("literal: %q", out)
	}
	if out := resultText(t, execTool(t, g, `{"pattern":"upper","ignoreCase":true}`)); !strings.Contains(out, "f.txt:2") {
		t.Fatalf("ignoreCase: %q", out)
	}
}

func TestParseRgJSONStopsAtLimit(t *testing.T) {
	var b strings.Builder
	for i := 1; i <= 5; i++ {
		fmt.Fprintf(&b, `{"type":"match","data":{"path":{"text":"f.go"},"line_number":%d,"lines":{"text":"hit %d\n"}}}`+"\n", i, i)
	}
	lines, matches, hit := parseRgJSON(strings.NewReader(b.String()), 3)
	if matches != 3 || !hit || len(lines) != 3 {
		t.Fatalf("matches=%d hit=%v lines=%d", matches, hit, len(lines))
	}
	if lines[0] != "f.go:1:hit 1" {
		t.Fatalf("lines[0]=%q", lines[0])
	}
}

func TestParseRgJSONContextLines(t *testing.T) {
	input := `{"type":"context","data":{"path":{"text":"f.go"},"line_number":1,"lines":{"text":"before\n"}}}
{"type":"match","data":{"path":{"text":"f.go"},"line_number":2,"lines":{"text":"hit\n"}}}
`
	lines, matches, _ := parseRgJSON(strings.NewReader(input), 10)
	if matches != 1 || len(lines) != 2 || lines[0] != "f.go-1-before" || lines[1] != "f.go:2:hit" {
		t.Fatalf("lines=%v matches=%d", lines, matches)
	}
}
