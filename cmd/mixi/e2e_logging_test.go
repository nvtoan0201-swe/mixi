package main

import (
	"strings"
	"testing"
)

// TestE2EVerboseMirrorCarriesStandardLogFields: a headless run with
// --verbose mirrors INFO records to stderr; the turn-lifecycle records must
// carry the standard field set (component, session_id, turn) end to end.
func TestE2EVerboseMirrorCarriesStandardLogFields(t *testing.T) {
	work := t.TempDir()
	script := fauxScript(t, textTurn("hello"))
	code, stdout, stderr := runMixi(t, script, work, "",
		"-p", "hi", "--model", "faux/scripted", "--no-save",
		"--log-level", "info", "--verbose")
	if code != 0 {
		t.Fatalf("exit = %d (stderr: %s)", code, stderr)
	}
	if !strings.Contains(stdout, "hello") {
		t.Errorf("stdout = %q, want the reply", stdout)
	}
	for _, want := range []string{"component=agent", "session_id=", "turn=1", "agent: turn end"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr missing %q:\n%s", want, stderr)
		}
	}
}

// TestE2EDefaultMirrorStaysQuiet: without --verbose the stderr mirror shows
// WARN+ only — a clean run logs nothing to the terminal.
func TestE2EDefaultMirrorStaysQuiet(t *testing.T) {
	work := t.TempDir()
	script := fauxScript(t, textTurn("hello"))
	code, _, stderr := runMixi(t, script, work, "",
		"-p", "hi", "--model", "faux/scripted", "--no-save", "--log-level", "info")
	if code != 0 {
		t.Fatalf("exit = %d (stderr: %s)", code, stderr)
	}
	if strings.Contains(stderr, "agent: turn") {
		t.Errorf("INFO records leaked to stderr without --verbose:\n%s", stderr)
	}
}
