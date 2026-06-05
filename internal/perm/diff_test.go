package perm

import (
	"strings"
	"testing"
)

func TestUnifiedDiffIdentical(t *testing.T) {
	if d := UnifiedDiff("f.txt", "a\nb\n", "a\nb\n"); d != "" {
		t.Fatalf("identical contents produced a diff:\n%s", d)
	}
}

func TestUnifiedDiffSimpleChange(t *testing.T) {
	old := "one\ntwo\nthree\n"
	new := "one\nTWO\nthree\n"
	d := UnifiedDiff("f.txt", old, new)
	for _, want := range []string{"--- a/f.txt", "+++ b/f.txt", "@@ -1,3 +1,3 @@", "-two", "+TWO", " one", " three"} {
		if !strings.Contains(d, want) {
			t.Errorf("diff missing %q:\n%s", want, d)
		}
	}
}

func TestUnifiedDiffAddAndDelete(t *testing.T) {
	d := UnifiedDiff("f", "a\nb\nc\n", "a\nc\nd\n")
	if !strings.Contains(d, "-b") || !strings.Contains(d, "+d") {
		t.Fatalf("diff missed delete/insert:\n%s", d)
	}
}

func TestUnifiedDiffNewAndEmptyFile(t *testing.T) {
	d := UnifiedDiff("f", "", "hello\nworld\n")
	if !strings.Contains(d, "+hello") || !strings.Contains(d, "+world") {
		t.Fatalf("creation diff wrong:\n%s", d)
	}
	d = UnifiedDiff("f", "hello\n", "")
	if !strings.Contains(d, "-hello") {
		t.Fatalf("deletion diff wrong:\n%s", d)
	}
}

func TestUnifiedDiffSeparatedChangesSplitHunks(t *testing.T) {
	var oldB, newB strings.Builder
	for i := 0; i < 30; i++ {
		line := string(rune('a' + i%26))
		oldB.WriteString(line + "\n")
		if i == 2 {
			newB.WriteString("CHANGED1\n")
		} else if i == 27 {
			newB.WriteString("CHANGED2\n")
		} else {
			newB.WriteString(line + "\n")
		}
	}
	d := UnifiedDiff("f", oldB.String(), newB.String())
	if got := strings.Count(d, "@@ -"); got != 2 {
		t.Fatalf("want 2 hunks for far-apart changes, got %d:\n%s", got, d)
	}
	if !strings.Contains(d, "+CHANGED1") || !strings.Contains(d, "+CHANGED2") {
		t.Fatalf("hunks missing changes:\n%s", d)
	}
}

// applyDiffOps reconstructs both sides from the edit script — the diff is
// correct iff both reconstructions round-trip.
func TestMyersDiffRoundTrip(t *testing.T) {
	cases := [][2]string{
		{"a\nb\nc", "a\nx\nc"},
		{"", "a\nb"},
		{"a\nb", ""},
		{"x\ny\nz", "z\ny\nx"},
		{"same", "same"},
		{"1\n2\n3\n4\n5", "0\n1\n3\n5\n6"},
	}
	for _, c := range cases {
		a, b := splitDiffLines(c[0]), splitDiffLines(c[1])
		ops := myersDiff(a, b)
		var gotA, gotB []string
		for _, op := range ops {
			switch op.kind {
			case ' ':
				gotA, gotB = append(gotA, op.line), append(gotB, op.line)
			case '-':
				gotA = append(gotA, op.line)
			case '+':
				gotB = append(gotB, op.line)
			}
		}
		if strings.Join(gotA, "\n") != strings.Join(a, "\n") {
			t.Errorf("old side does not round-trip for %q→%q", c[0], c[1])
		}
		if strings.Join(gotB, "\n") != strings.Join(b, "\n") {
			t.Errorf("new side does not round-trip for %q→%q", c[0], c[1])
		}
	}
}
