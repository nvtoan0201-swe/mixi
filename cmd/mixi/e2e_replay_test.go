package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// replayFixturePath resolves the shared example session relative to this
// package directory (tests run with cwd = package dir).
func replayFixturePath(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("..", "..", "internal", "obs", "testdata", "example_session.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(abs); err != nil {
		t.Fatal(err)
	}
	return abs
}

// TestE2EReplayInstant: `mixi replay <file> --speed instant` renders the
// recorded transcript to stdout, exits 0, makes zero API calls (no provider
// key in the isolated environment), and logs nothing to stderr.
func TestE2EReplayInstant(t *testing.T) {
	code, stdout, stderr := runMixi(t, "", t.TempDir(), "",
		"replay", replayFixturePath(t), "--speed", "instant")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", code, stderr)
	}
	for _, want := range []string{
		"> add a --version flag",
		"⏺ grep",
		"Added the --version flag.",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout missing %q:\n%s", want, stdout)
		}
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty", stderr)
	}
}

// TestE2EReplayUntil cuts the transcript at the named entry.
func TestE2EReplayUntil(t *testing.T) {
	code, stdout, _ := runMixi(t, "", t.TempDir(), "",
		"replay", replayFixturePath(t), "--speed", "instant", "--until", "c3d4e5f6")
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if strings.Contains(stdout, "Added the --version flag.") {
		t.Errorf("content after --until leaked:\n%s", stdout)
	}
}

// TestE2EReplayMissingFile exits with a usage error.
func TestE2EReplayMissingFile(t *testing.T) {
	code, _, stderr := runMixi(t, "", t.TempDir(), "",
		"replay", "/nonexistent/session.jsonl")
	if code != 2 {
		t.Fatalf("exit = %d, want 2 (stderr: %s)", code, stderr)
	}
	if !strings.Contains(stderr, "mixi:") {
		t.Errorf("stderr = %q, want an error message", stderr)
	}
}

// TestE2EReplayNoFileArg exits with usage when the file argument is absent.
func TestE2EReplayNoFileArg(t *testing.T) {
	code, _, stderr := runMixi(t, "", t.TempDir(), "", "replay")
	if code != 2 || !strings.Contains(stderr, "replay needs a session file") {
		t.Fatalf("exit = %d stderr = %q, want usage error", code, stderr)
	}
}
