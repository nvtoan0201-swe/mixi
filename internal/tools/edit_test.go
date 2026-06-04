package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type editCorpusCase struct {
	Name            string     `json:"name"`
	Input           string     `json:"input"`
	Edits           []editSpec `json:"edits"`
	Want            string     `json:"want"`
	WantErrContains string     `json:"wantErrContains"`
}

func TestEditCorpus(t *testing.T) {
	files, err := filepath.Glob("testdata/edit_corpus/*.json")
	if err != nil || len(files) == 0 {
		t.Fatalf("no corpus files: %v", err)
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var corpus struct {
			Cases []editCorpusCase `json:"cases"`
		}
		if err := json.Unmarshal(data, &corpus); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		for _, tc := range corpus.Cases {
			t.Run(filepath.Base(f)+"/"+tc.Name, func(t *testing.T) {
				runEditCase(t, tc)
			})
		}
	}
}

func runEditCase(t *testing.T, tc editCorpusCase) {
	dir := t.TempDir()
	path := filepath.Join(dir, "target.txt")
	if err := os.WriteFile(path, []byte(tc.Input), 0o644); err != nil {
		t.Fatal(err)
	}
	ed := &editTool{cwd: dir, mq: newMutQueue()}
	args, _ := json.Marshal(map[string]any{"path": path, "edits": tc.Edits})
	res := execTool(t, ed, string(args))

	if tc.WantErrContains != "" {
		if !res.IsError {
			t.Fatalf("want error containing %q, got success", tc.WantErrContains)
		}
		if got := resultText(t, res); !strings.Contains(got, tc.WantErrContains) {
			t.Fatalf("error %q does not contain %q", got, tc.WantErrContains)
		}
		// Failed edits must leave the file untouched.
		data, _ := os.ReadFile(path)
		if string(data) != tc.Input {
			t.Fatal("file modified despite validation error")
		}
		return
	}
	if res.IsError {
		t.Fatalf("unexpected error: %q", resultText(t, res))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != tc.Want {
		t.Fatalf("file content:\n got %q\nwant %q", data, tc.Want)
	}
}

func TestEditMissingFile(t *testing.T) {
	ed := &editTool{cwd: t.TempDir(), mq: newMutQueue()}
	res := execTool(t, ed, `{"path":"ghost.txt","edits":[{"oldText":"a","newText":"b"}]}`)
	if !res.IsError || !strings.Contains(resultText(t, res), "no such file") {
		t.Fatalf("res=%q", resultText(t, res))
	}
}

func TestEditEmptyOldTextRejected(t *testing.T) {
	dir := t.TempDir()
	path := writeTemp(t, "f.txt", "content")
	ed := &editTool{cwd: dir, mq: newMutQueue()}
	res := execTool(t, ed, fmt.Sprintf(`{"path":%q,"edits":[{"oldText":"","newText":"b"}]}`, path))
	if !res.IsError || !strings.Contains(resultText(t, res), "edits[0].oldText is empty") {
		t.Fatalf("res=%q", resultText(t, res))
	}
}

func TestEditDetailsShape(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	os.WriteFile(path, []byte("line1\nline2\nline3\n"), 0o644)
	ed := &editTool{cwd: dir, mq: newMutQueue()}
	res := execTool(t, ed, fmt.Sprintf(`{"path":%q,"edits":[{"oldText":"line2","newText":"LINE2\nLINE2b"}]}`, path))
	if res.IsError {
		t.Fatalf("err: %q", resultText(t, res))
	}
	var d struct {
		Diff             string `json:"diff"`
		Patch            string `json:"patch"`
		FirstChangedLine int    `json:"firstChangedLine"`
	}
	if err := json.Unmarshal(res.Details, &d); err != nil {
		t.Fatalf("details: %v", err)
	}
	if d.FirstChangedLine != 2 {
		t.Fatalf("firstChangedLine=%d, want 2", d.FirstChangedLine)
	}
	if !strings.Contains(d.Patch, "-line2") || !strings.Contains(d.Patch, "+LINE2") {
		t.Fatalf("patch=%q", d.Patch)
	}
}

func TestNormalizeForMatchTable(t *testing.T) {
	cases := map[string]string{
		"“x”":         `"x"`,
		"‘y’":         "'y'",
		"a–b—c":       "a-b-c",
		"end  \nnext": "end\nnext",
		"ﬁle":         "file", // NFKC ligature fold
	}
	for in, want := range cases {
		if got := normalizeForMatch(in); got != want {
			t.Errorf("normalize(%q)=%q want %q", in, got, want)
		}
	}
}
