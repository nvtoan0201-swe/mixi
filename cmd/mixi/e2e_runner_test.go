package main

// End-to-end harness: each test re-executes this test binary as the real
// mixi process (TestMain dispatches to main() when MIXI_E2E_CHILD is set).
// A subprocess per test is required — the faux provider loads its script
// once per process — and it lets tests deliver real signals.

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const childEnv = "MIXI_E2E_CHILD"

func TestMain(m *testing.M) {
	if os.Getenv(childEnv) == "1" {
		main() // exits with mixi's real exit code
	}
	os.Exit(m.Run())
}

// fauxScript marshals turns to a script file and returns its path.
func fauxScript(t *testing.T, turns ...map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"turns": turns})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "script.json")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func textTurn(text string) map[string]any { return map[string]any{"text": text} }

func toolCallTurn(id, name string, args map[string]any) map[string]any {
	rawArgs, _ := json.Marshal(args)
	return map[string]any{"toolCalls": []map[string]any{
		{"id": id, "name": name, "args": json.RawMessage(rawArgs)},
	}}
}

// mixiCmd builds the child process: isolated HOME (no real ~/.mixi
// settings), cwd pinned to workDir, faux script wired via env.
func mixiCmd(t *testing.T, script, workDir string, args ...string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0], args...)
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(),
		childEnv+"=1",
		"HOME="+t.TempDir(),
		"MIXI_FAUX_SCRIPT="+script,
	)
	return cmd
}

// runMixi runs the child to completion and returns exit code and output.
func runMixi(t *testing.T, script, workDir, stdin string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	cmd := mixiCmd(t, script, workDir, args...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var out, errOut strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	err := cmd.Run()
	code = cmd.ProcessState.ExitCode()
	if err != nil && code == -1 {
		t.Fatalf("run mixi: %v (stderr: %s)", err, errOut.String())
	}
	return code, out.String(), errOut.String()
}

// sessionFiles globs the session JSONL files written for workDir.
func sessionFiles(t *testing.T, sessionDir string) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(sessionDir, "*", "*.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	return matches
}
