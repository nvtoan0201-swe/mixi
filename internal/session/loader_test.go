package session

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// buildSession writes a small valid session and returns its path plus the
// appended entries.
func buildSession(t *testing.T) (string, []*MessageEntry) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "crash.jsonl")
	s, err := NewJSONL(path, Header{CWD: "/tmp/x"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	msgs := []*MessageEntry{userEntry("one"), userEntry("two"), userEntry("three")}
	for _, m := range msgs {
		if err := s.Append(m); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	return path, msgs
}

// captureWarns routes slog through a buffer for the test's duration.
func captureWarns(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })
	return &buf
}

func TestCrashTailRecovery(t *testing.T) {
	path, msgs := buildSession(t)
	// Simulate kill -9 mid-append: chop the file inside the last line.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data[:len(data)-20], 0o600); err != nil {
		t.Fatal(err)
	}

	warns := captureWarns(t)
	s, err := OpenJSONL(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if !strings.Contains(warns.String(), "partial trailing line") {
		t.Fatalf("no WARN logged: %q", warns.String())
	}
	// Data before the tail is intact; the torn entry is gone.
	if n := len(s.Entries()); n != 2 {
		t.Fatalf("entries = %d, want 2", n)
	}
	if s.LeafID() != msgs[1].EntryID() {
		t.Fatalf("leaf = %q, want %q", s.LeafID(), msgs[1].EntryID())
	}
	// The partial tail was truncated away: appends start on a clean line.
	c := userEntry("recovered")
	if err := s.Append(c); err != nil {
		t.Fatal(err)
	}
	s.Close()
	r, err := OpenJSONL(path, Options{ReadOnly: true})
	if err != nil {
		t.Fatalf("reopen after recovery append: %v", err)
	}
	defer r.Close()
	if n := len(r.Entries()); n != 3 {
		t.Fatalf("entries after recovery append = %d, want 3", n)
	}
}

func TestCrashTailReadOnlySkipsWithoutTruncating(t *testing.T) {
	path, _ := buildSession(t)
	data, _ := os.ReadFile(path)
	torn := data[:len(data)-20]
	if err := os.WriteFile(path, torn, 0o600); err != nil {
		t.Fatal(err)
	}

	captureWarns(t)
	s, err := OpenJSONL(path, Options{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	after, _ := os.ReadFile(path)
	if !bytes.Equal(after, torn) {
		t.Fatal("read-only open modified the file")
	}
}

func TestMissingTrailingNewlineKeepsValidEntry(t *testing.T) {
	path, _ := buildSession(t)
	data, _ := os.ReadFile(path)
	// Crash after the JSON but before the newline made it to disk.
	if err := os.WriteFile(path, data[:len(data)-1], 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := OpenJSONL(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if n := len(s.Entries()); n != 3 {
		t.Fatalf("entries = %d, want 3 (valid newline-less tail dropped?)", n)
	}
	// The newline was repaired, so an append does not glue onto the tail.
	if err := s.Append(userEntry("four")); err != nil {
		t.Fatal(err)
	}
	s.Close()
	r, err := OpenJSONL(path, Options{ReadOnly: true})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer r.Close()
	if n := len(r.Entries()); n != 4 {
		t.Fatalf("entries = %d, want 4", n)
	}
}

func TestMidFileCorruptionIsFatal(t *testing.T) {
	path, _ := buildSession(t)
	data, _ := os.ReadFile(path)
	lines := bytes.Split(bytes.TrimRight(data, "\n"), []byte("\n"))
	lines[2] = []byte(`{"type":"message","id":"broken`) // torn line with data after it
	if err := os.WriteFile(path, append(bytes.Join(lines, []byte("\n")), '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenJSONL(path, Options{}); err == nil {
		t.Fatal("mid-file corruption accepted")
	}
}

func TestNewerVersionRejectedWithHint(t *testing.T) {
	path := filepath.Join(t.TempDir(), "future.jsonl")
	content := `{"type":"session","version":2,"id":"x","timestamp":"2026-06-03T06:30:00.000Z","cwd":"/tmp","parentSession":""}` + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := OpenJSONL(path, Options{})
	if err == nil || !strings.Contains(err.Error(), "upgrade mixi") {
		t.Fatalf("err = %v", err)
	}
}

func TestHeaderlessFileRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "junk.jsonl")
	if err := os.WriteFile(path, []byte("not json at all"), 0o600); err != nil {
		t.Fatal(err)
	}
	captureWarns(t)
	if _, err := OpenJSONL(path, Options{}); err == nil {
		t.Fatal("headerless file accepted")
	}
	// The stranger's file must not be modified (no truncate, no repair).
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != "not json at all" {
		t.Fatalf("non-session file was modified: %q", after)
	}
}

func TestOversizedLineRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "big.jsonl")
	head, err := MarshalHeader(Header{Version: 1, ID: "x", Timestamp: "2026-06-03T06:30:00.000Z", CWD: "/tmp"})
	if err != nil {
		t.Fatal(err)
	}
	big := append(append(head, '\n'), bytes.Repeat([]byte("a"), maxLineBytes+1)...)
	if err := os.WriteFile(path, append(big, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = OpenJSONL(path, Options{})
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("err = %v", err)
	}
}
