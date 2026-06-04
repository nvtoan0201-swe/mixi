package tools

import (
	"strings"
	"testing"
)

func TestHeadTruncateLineLimit(t *testing.T) {
	lines := make([]string, 10)
	for i := range lines {
		lines[i] = "x"
	}
	kept, trunc := headTruncate(lines, 3, 1000)
	if len(kept) != 3 || !trunc {
		t.Fatalf("got %d lines, trunc=%v; want 3, true", len(kept), trunc)
	}
	kept, trunc = headTruncate(lines, 100, 1000)
	if len(kept) != 10 || trunc {
		t.Fatalf("got %d lines, trunc=%v; want 10, false", len(kept), trunc)
	}
}

func TestHeadTruncateByteLimit(t *testing.T) {
	lines := []string{strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("c", 40)}
	kept, trunc := headTruncate(lines, 100, 90)
	if len(kept) != 2 || !trunc {
		t.Fatalf("got %d lines, trunc=%v; want 2, true", len(kept), trunc)
	}
}

func TestHeadTruncateOversizedFirstLine(t *testing.T) {
	kept, trunc := headTruncate([]string{strings.Repeat("a", 100)}, 100, 50)
	if len(kept) != 0 || !trunc {
		t.Fatalf("got %d lines, trunc=%v; want 0, true", len(kept), trunc)
	}
}

func TestTailTruncateKeepsEnd(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 100; i++ {
		b.WriteString("line\n")
	}
	b.WriteString("LAST")
	out, trunc := tailTruncate(b.String(), 10, 1<<20)
	if !trunc || !strings.HasSuffix(out, "LAST") {
		t.Fatalf("trunc=%v out=%q", trunc, out)
	}
	if got := strings.Count(out, "\n") + 1; got != 10 {
		t.Fatalf("kept %d lines, want 10", got)
	}
}

func TestTailTruncateUTF8Boundary(t *testing.T) {
	s := strings.Repeat("é", 1000)         // 2-byte runes, no newlines
	out, trunc := tailTruncate(s, 10, 101) // 101 forces a mid-rune cut
	if !trunc {
		t.Fatal("expected truncation")
	}
	for _, r := range out {
		if r == '�' {
			t.Fatal("output contains replacement char — split a UTF-8 sequence")
		}
	}
}

func TestClipLineRuneBoundary(t *testing.T) {
	if got := clipLine("héllo", 3); strings.ContainsRune(got, '�') {
		t.Fatalf("clipLine split a rune: %q", got)
	}
	if got := clipLine("abc", 10); got != "abc" {
		t.Fatalf("clipLine mangled short input: %q", got)
	}
}
