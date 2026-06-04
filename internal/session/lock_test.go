//go:build unix

package session

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// flock is held per open file description, so a second OpenJSONL in the
// same process contends exactly like a second process would.

func TestSecondOpenRefusedWhileLocked(t *testing.T) {
	path, _ := buildSession(t)

	first, err := OpenJSONL(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()

	_, err = OpenJSONL(path, Options{})
	if err == nil {
		t.Fatal("second writer open succeeded while locked")
	}
	if !strings.Contains(err.Error(), "in use by another process") {
		t.Fatalf("err = %v", err)
	}
	// The pid hint names the holder (us).
	if !strings.Contains(err.Error(), fmt.Sprintf("(pid %d)", os.Getpid())) {
		t.Fatalf("err lacks pid hint: %v", err)
	}
}

func TestReadOnlyBypassesLock(t *testing.T) {
	path, _ := buildSession(t)

	first, err := OpenJSONL(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()

	ro, err := OpenJSONL(path, Options{ReadOnly: true})
	if err != nil {
		t.Fatalf("read-only open refused: %v", err)
	}
	defer ro.Close()
	if n := len(ro.Entries()); n != 3 {
		t.Fatalf("read-only entries = %d, want 3", n)
	}
	if err := ro.Append(userEntry("nope")); err == nil {
		t.Fatal("append on read-only store succeeded")
	}
}

func TestCloseReleasesLock(t *testing.T) {
	path, _ := buildSession(t)

	first, err := OpenJSONL(path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := OpenJSONL(path, Options{})
	if err != nil {
		t.Fatalf("reopen after Close: %v", err)
	}
	second.Close()
	// The pid hint sidecar is cleaned up too.
	if _, err := os.Stat(path + ".lock"); !os.IsNotExist(err) {
		t.Fatalf("hint file left behind (stat err = %v)", err)
	}
}

func TestDeferredSessionLocksAtFirstWrite(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/d.jsonl"
	s, err := NewJSONL(path, Header{CWD: "/tmp/x"}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Append(userEntry("hi")); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenJSONL(path, Options{}); err == nil || !strings.Contains(err.Error(), "in use") {
		t.Fatalf("flushed deferred session not locked: %v", err)
	}
}
