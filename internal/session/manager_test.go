package session

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testManager(t *testing.T) *Manager {
	t.Helper()
	return &Manager{Root: t.TempDir()}
}

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"/home/u/proj":    "--home-u-proj--",
		`C:\Users\u\proj`: "--C--Users-u-proj--",
		"/":               "----",
		"/a/b c/d":        "--a-b c-d--",
	}
	for in, want := range cases {
		if got := slugify(in); got != want {
			t.Errorf("slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestManagerCreateAndContinueRecent(t *testing.T) {
	m := testManager(t)
	cwd := "/home/u/proj"

	s, err := m.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	// Deferred: nothing on disk yet, so nothing to continue.
	if _, err := m.ContinueRecent(cwd); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ContinueRecent before flush: %v", err)
	}
	a := userEntry("first")
	if err := s.Append(a); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	files, err := filepath.Glob(filepath.Join(m.Dir(cwd), "*.jsonl"))
	if err != nil || len(files) != 1 {
		t.Fatalf("session files = %v (err %v)", files, err)
	}
	if !strings.Contains(filepath.Base(files[0]), "_") {
		t.Fatalf("filename %q lacks ts_uuid shape", files[0])
	}

	r, err := m.ContinueRecent(cwd)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if r.LeafID() != a.EntryID() {
		t.Fatalf("continued leaf = %q, want %q", r.LeafID(), a.EntryID())
	}
}

func TestContinueRecentPicksNewest(t *testing.T) {
	m := testManager(t)
	cwd := "/home/u/proj"
	var lastLeaf string
	for i := 0; i < 2; i++ {
		s, err := m.Create(cwd)
		if err != nil {
			t.Fatal(err)
		}
		e := userEntry("hello")
		if err := s.Append(e); err != nil {
			t.Fatal(err)
		}
		lastLeaf = e.EntryID()
		s.Close()
		time.Sleep(20 * time.Millisecond) // distinct mtimes
	}
	r, err := m.ContinueRecent(cwd)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if r.LeafID() != lastLeaf {
		t.Fatalf("continued wrong session: leaf %q, want %q", r.LeafID(), lastLeaf)
	}
}

func TestForkCopiesPrefixAndSetsLeaf(t *testing.T) {
	m := testManager(t)
	cwd := "/home/u/proj"
	s, err := m.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	a, b, c := userEntry("one"), userEntry("two"), userEntry("three")
	for _, e := range []Entry{a, b, c} {
		if err := s.Append(e); err != nil {
			t.Fatal(err)
		}
	}
	srcPath := s.(*jsonlStore).Path()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	fork, err := m.Fork(srcPath, b.EntryID())
	if err != nil {
		t.Fatal(err)
	}
	defer fork.Close()

	if fork.Header().ParentSession != srcPath {
		t.Fatalf("parentSession = %q", fork.Header().ParentSession)
	}
	if fork.Header().ID == "" || fork.Header().ID == "0190a1b2-7c3d-7e4f-8a5b-6c7d8e9f0a1b" {
		t.Fatalf("fork did not get its own session id")
	}
	if fork.LeafID() != b.EntryID() {
		t.Fatalf("fork leaf = %q, want %q", fork.LeafID(), b.EntryID())
	}
	if _, ok := fork.Get(c.EntryID()); ok {
		t.Fatal("entry after fork point was copied")
	}
	// IDs in the copied prefix are preserved verbatim.
	if _, ok := fork.Get(a.EntryID()); !ok {
		t.Fatal("prefix entry missing from fork")
	}
	// Forking does not disturb the source.
	src, err := OpenJSONL(srcPath, Options{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	if n := len(src.Entries()); n != 3 {
		t.Fatalf("source entries = %d, want 3", n)
	}
}

func TestForkWhileSourceLocked(t *testing.T) {
	m := testManager(t)
	cwd := "/home/u/proj"
	s, err := m.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a := userEntry("one")
	if err := s.Append(a); err != nil {
		t.Fatal(err)
	}
	// Source stays open (locked); fork reads without the lock.
	fork, err := m.Fork(s.(*jsonlStore).Path(), a.EntryID())
	if err != nil {
		t.Fatalf("fork of locked source: %v", err)
	}
	fork.Close()
}

func TestForkUnknownEntry(t *testing.T) {
	m := testManager(t)
	s, err := m.Create("/home/u/proj")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Append(userEntry("x")); err != nil {
		t.Fatal(err)
	}
	path := s.(*jsonlStore).Path()
	s.Close()
	if _, err := m.Fork(path, "deadbeef"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("err = %v", err)
	}
}

func TestInMemoryNeverTouchesDisk(t *testing.T) {
	m := testManager(t)
	s := m.InMemory("/home/u/proj")
	defer s.Close()
	if err := s.Append(userEntry("ephemeral")); err != nil {
		t.Fatal(err)
	}
	ents, err := os.ReadDir(m.Root)
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 0 {
		t.Fatalf("in-memory session wrote to disk: %v", ents)
	}
}
