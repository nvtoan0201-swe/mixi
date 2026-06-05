package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestE2EDefaultModeDeniesWriteHeadless: prompt mode (the default) has no
// interactive asker, so a write resolves ASK→deny with an actionable reason
// while the run itself still completes.
func TestE2EDefaultModeDeniesWriteHeadless(t *testing.T) {
	work := t.TempDir()
	script := fauxScript(t,
		toolCallTurn("call-1", "write", map[string]any{"path": "out.txt", "content": "x"}),
		textTurn("done"),
	)
	code, stdout, stderr := runMixi(t, script, work, "",
		"-p", "write out.txt", "--model", "faux/scripted", "--no-save", "--output", "json")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", code, stderr)
	}
	if _, err := os.Stat(filepath.Join(work, "out.txt")); err == nil {
		t.Fatal("write executed despite headless prompt mode")
	}
	if !strings.Contains(stdout, "Permission denied") || !strings.Contains(stdout, "--permission-mode") {
		t.Fatalf("tool result lacks actionable denial reason:\n%s", stdout)
	}
}

// TestE2EYoloBypassesPermissionGate: --permission-mode yolo lets the same
// write run unprompted.
func TestE2EYoloBypassesPermissionGate(t *testing.T) {
	work := t.TempDir()
	script := fauxScript(t,
		toolCallTurn("call-1", "write", map[string]any{"path": "out.txt", "content": "x"}),
		textTurn("done"),
	)
	code, _, stderr := runMixi(t, script, work, "",
		"-p", "write out.txt", "--model", "faux/scripted", "--no-save",
		"--permission-mode", "yolo")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", code, stderr)
	}
	if got, err := os.ReadFile(filepath.Join(work, "out.txt")); err != nil || string(got) != "x" {
		t.Fatalf("out.txt = %q, %v; want \"x\"", got, err)
	}
}

// TestE2EDenyRuleBlocksWriteEvenInYolo: explicit deny rules outrank the
// yolo mode default; the project settings file carries the rule.
func TestE2EDenyRuleBlocksWriteEvenInYolo(t *testing.T) {
	work := t.TempDir()
	if err := os.MkdirAll(filepath.Join(work, ".mixi"), 0o755); err != nil {
		t.Fatal(err)
	}
	settings := `{"permissions": {"deny": ["write(**/blocked.txt)"]}}`
	if err := os.WriteFile(filepath.Join(work, ".mixi", "settings.json"), []byte(settings), 0o644); err != nil {
		t.Fatal(err)
	}
	script := fauxScript(t,
		toolCallTurn("call-1", "write", map[string]any{"path": "blocked.txt", "content": "x"}),
		textTurn("done"),
	)
	code, stdout, stderr := runMixi(t, script, work, "",
		"-p", "write blocked.txt", "--model", "faux/scripted", "--no-save",
		"--permission-mode", "yolo", "--output", "json")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", code, stderr)
	}
	if _, err := os.Stat(filepath.Join(work, "blocked.txt")); err == nil {
		t.Fatal("deny rule did not block the write")
	}
	if !strings.Contains(stdout, "blocked by deny rule") {
		t.Fatalf("denial does not name the rule:\n%s", stdout)
	}
}

// TestE2ESecretGlobReadDeniedHeadless: reading .env is a forced ask even
// though prompt mode allows reads, and headless asks resolve to deny.
func TestE2ESecretGlobReadDeniedHeadless(t *testing.T) {
	work := t.TempDir()
	if err := os.WriteFile(filepath.Join(work, ".env"), []byte("API_KEY=s3cret"), 0o644); err != nil {
		t.Fatal(err)
	}
	script := fauxScript(t,
		toolCallTurn("call-1", "read", map[string]any{"path": ".env"}),
		textTurn("done"),
	)
	code, stdout, stderr := runMixi(t, script, work, "",
		"-p", "read .env", "--model", "faux/scripted", "--no-save", "--output", "json")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", code, stderr)
	}
	if strings.Contains(stdout, "s3cret") {
		t.Fatal("secret file content leaked into the transcript")
	}
	if !strings.Contains(stdout, "Permission denied") || !strings.Contains(stdout, "secrets") {
		t.Fatalf("secret read not denied with reason:\n%s", stdout)
	}
}
