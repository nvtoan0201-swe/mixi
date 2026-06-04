package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/user/mixi-agent/internal/ai"
)

func execTool(t *testing.T, tool Tool, args string) ToolResult {
	t.Helper()
	res, err := tool.Execute(context.Background(), json.RawMessage(args), nil)
	if err != nil {
		t.Fatalf("%s execute: %v", tool.Name(), err)
	}
	return res
}

func resultText(t *testing.T, res ToolResult) string {
	t.Helper()
	if len(res.Content) != 1 {
		t.Fatalf("want 1 content block, got %d", len(res.Content))
	}
	tc, ok := res.Content[0].(ai.TextContent)
	if !ok {
		t.Fatalf("content is %T, not TextContent", res.Content[0])
	}
	return tc.Text
}

func writeTemp(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReadLinePrefixesAndOffset(t *testing.T) {
	path := writeTemp(t, "f.txt", "alpha\nbeta\ngamma\n")
	rd := &readTool{}
	out := resultText(t, execTool(t, rd, fmt.Sprintf(`{"path":%q}`, path)))
	if out != "1→alpha\n2→beta\n3→gamma" {
		t.Fatalf("out=%q", out)
	}
	out = resultText(t, execTool(t, rd, fmt.Sprintf(`{"path":%q,"offset":2,"limit":1}`, path)))
	if !strings.HasPrefix(out, "2→beta") {
		t.Fatalf("out=%q", out)
	}
	if !strings.Contains(out, "[Showing lines 2–2 of 3. Use offset=3 to continue.]") {
		t.Fatalf("missing continuation hint: %q", out)
	}
}

func TestReadMissingFile(t *testing.T) {
	res := execTool(t, &readTool{}, `{"path":"/nonexistent/zz.txt"}`)
	if !res.IsError || !strings.Contains(resultText(t, res), "no such file") {
		t.Fatalf("res=%+v", res)
	}
}

func TestReadOffsetBeyondEOF(t *testing.T) {
	path := writeTemp(t, "f.txt", "one\ntwo\n")
	res := execTool(t, &readTool{}, fmt.Sprintf(`{"path":%q,"offset":10}`, path))
	if !res.IsError || resultText(t, res) != "Offset 10 is beyond end of file (2 lines total)" {
		t.Fatalf("res=%q", resultText(t, res))
	}
}

func TestReadEmptyFile(t *testing.T) {
	path := writeTemp(t, "f.txt", "")
	if got := resultText(t, execTool(t, &readTool{}, fmt.Sprintf(`{"path":%q}`, path))); got != "(empty file)" {
		t.Fatalf("got=%q", got)
	}
}

func TestReadBinaryFile(t *testing.T) {
	path := writeTemp(t, "f.bin", "ab\x00cd")
	res := execTool(t, &readTool{}, fmt.Sprintf(`{"path":%q}`, path))
	if !res.IsError || !strings.Contains(resultText(t, res), "binary file") {
		t.Fatalf("res=%q", resultText(t, res))
	}
}

func TestReadTruncatesAtMaxLines(t *testing.T) {
	var b strings.Builder
	for i := 0; i < MaxLines+500; i++ {
		b.WriteString("line\n")
	}
	path := writeTemp(t, "big.txt", b.String())
	out := resultText(t, execTool(t, &readTool{}, fmt.Sprintf(`{"path":%q}`, path)))
	want := fmt.Sprintf("[Showing lines 1–%d of %d. Use offset=%d to continue.]", MaxLines, MaxLines+500, MaxLines+1)
	if !strings.Contains(out, want) {
		t.Fatalf("missing %q in tail: %q", want, out[len(out)-120:])
	}
}

func TestReadOversizedSingleLine(t *testing.T) {
	path := writeTemp(t, "wide.txt", strings.Repeat("a", MaxBytes+1))
	res := execTool(t, &readTool{}, fmt.Sprintf(`{"path":%q}`, path))
	out := resultText(t, res)
	if !res.IsError || !strings.Contains(out, "sed -n '1p'") {
		t.Fatalf("res=%q", out)
	}
}

func TestReadImagePassthrough(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "img.png")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	res := execTool(t, &readTool{}, fmt.Sprintf(`{"path":%q}`, path))
	ic, ok := res.Content[0].(ai.ImageContent)
	if !ok || ic.MimeType != "image/png" || ic.Data == "" {
		t.Fatalf("res=%+v", res)
	}
}

func TestReadImageDownscaled(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, maxImageDim+500, 100))
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	out, mime, err := resizeImageIfNeeded(buf.Bytes(), "image/png")
	if err != nil {
		t.Fatal(err)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Width > maxImageDim || cfg.Height > maxImageDim || mime != "image/png" {
		t.Fatalf("resized to %dx%d mime=%s", cfg.Width, cfg.Height, mime)
	}
}

func TestSniffImageMime(t *testing.T) {
	cases := map[string]string{
		"\x89PNG\r\n\x1a\nrest": "image/png",
		"\xff\xd8\xffrest":      "image/jpeg",
		"GIF89a-rest":           "image/gif",
		"RIFF0000WEBPrest":      "image/webp",
		"plain text":            "",
	}
	for in, want := range cases {
		if got := sniffImageMime([]byte(in)); got != want {
			t.Errorf("sniff(%q)=%q want %q", in[:min(8, len(in))], got, want)
		}
	}
}
