package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeferredWriteNoFileUntilFirstMessage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "deferred.jsonl")
	s, err := NewJSONL(path, Header{CWD: "/tmp/x"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// Pre-message entries are buffered, indexed, and invisible on disk.
	mc := &ModelChangeEntry{Provider: "anthropic", ModelID: "claude-sonnet-4-6"}
	if err := s.Append(mc); err != nil {
		t.Fatal(err)
	}
	if err := s.Append(&ThinkingLevelChangeEntry{ThinkingLevel: "high"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("file exists before first message (stat err = %v)", err)
	}
	if _, ok := s.Get(mc.EntryID()); !ok {
		t.Fatal("buffered entry not queryable")
	}

	// First message flushes header + buffer + the message in one write.
	if err := s.Append(userEntry("hello")); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("file has %d lines, want 4 (header + 3 entries)", len(lines))
	}
	var head struct {
		Type    string `json:"type"`
		Version int    `json:"version"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &head); err != nil || head.Type != "session" || head.Version != 1 {
		t.Fatalf("header line = %q (err %v)", lines[0], err)
	}

	// Later appends land directly.
	if err := s.Append(userEntry("again")); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(path)
	if n := strings.Count(string(data), "\n"); n != 5 {
		t.Fatalf("file has %d lines after direct append, want 5", n)
	}
}

func TestReloadRebuildsTree(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reload.jsonl")
	s, err := NewJSONL(path, Header{CWD: "/tmp/x"}, Options{Fsync: true})
	if err != nil {
		t.Fatal(err)
	}
	a, b := userEntry("one"), userEntry("two")
	for _, e := range []Entry{a, b} {
		if err := s.Append(e); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SetLeaf(a.EntryID()); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	r, err := OpenJSONL(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if r.Header().CWD != "/tmp/x" {
		t.Fatalf("header = %+v", r.Header())
	}
	if r.LeafID() != a.EntryID() {
		t.Fatalf("replayed leaf = %q, want %q", r.LeafID(), a.EntryID())
	}
	if n := len(r.Entries()); n != 3 {
		t.Fatalf("entries = %d, want 3", n)
	}
	// Continue appending across the reload: parent is the replayed leaf.
	c := userEntry("three")
	if err := r.Append(c); err != nil {
		t.Fatal(err)
	}
	if c.ParentID() != a.EntryID() {
		t.Fatalf("parent = %q, want %q", c.ParentID(), a.EntryID())
	}
}

func TestPinnedSurvivesRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pinned.jsonl")
	s, err := NewJSONL(path, Header{CWD: "/tmp/x"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	e := userEntry("keep me")
	e.Pinned = true
	if err := s.Append(e); err != nil {
		t.Fatal(err)
	}
	s.Close()

	r, err := OpenJSONL(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	got, _ := r.Get(e.EntryID())
	if !got.(*MessageEntry).Pinned {
		t.Fatal("pinned flag lost")
	}
}

func TestEmptySessionLeavesNoLitter(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "never.jsonl")
	s, err := NewJSONL(path, Header{CWD: "/tmp/x"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Append(&SessionInfoEntry{Name: "named but empty"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 0 {
		t.Fatalf("empty session left files: %v", ents)
	}
}
