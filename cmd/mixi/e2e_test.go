package main

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestE2EToolCallWritesFileAndPersistsSession is the milestone scenario:
// a scripted tool-call turn drives the real write tool, the run persists a
// session JSONL, and the final assistant text lands on stdout.
func TestE2EToolCallWritesFileAndPersistsSession(t *testing.T) {
	work, sess := t.TempDir(), t.TempDir()
	script := fauxScript(t,
		toolCallTurn("call-1", "write", map[string]any{"path": "hello.txt", "content": "hi"}),
		textTurn("wrote hello.txt"),
	)
	code, stdout, stderr := runMixi(t, script, work, "",
		"-p", "create hello.txt containing hi",
		"--model", "faux/scripted", "--session-dir", sess)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", code, stderr)
	}
	if !strings.Contains(stdout, "wrote hello.txt") {
		t.Errorf("stdout = %q, want final assistant text", stdout)
	}
	got, err := os.ReadFile(filepath.Join(work, "hello.txt"))
	if err != nil || string(got) != "hi" {
		t.Errorf("hello.txt = %q, %v; want \"hi\"", got, err)
	}
	files := sessionFiles(t, sess)
	if len(files) != 1 {
		t.Fatalf("session files = %v, want exactly 1", files)
	}
	// Header + user + assistant(tool call) + tool result + assistant text.
	raw, _ := os.ReadFile(files[0])
	if lines := strings.Count(strings.TrimSpace(string(raw)), "\n") + 1; lines < 5 {
		t.Errorf("session has %d lines, want >= 5", lines)
	}
}

// TestE2EJSONOutputStreamsValidJSONL checks --output json: every stdout
// line is a JSON event record and the lifecycle frame is complete.
func TestE2EJSONOutputStreamsValidJSONL(t *testing.T) {
	script := fauxScript(t, textTurn("hello world"))
	code, stdout, stderr := runMixi(t, script, t.TempDir(), "",
		"-p", "hi", "--model", "faux/scripted", "--output", "json", "--no-save")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", code, stderr)
	}
	events := []string{}
	sc := bufio.NewScanner(strings.NewReader(stdout))
	for sc.Scan() {
		var rec struct {
			Event  string `json:"event"`
			Reason string `json:"reason"`
		}
		if err := json.Unmarshal(sc.Bytes(), &rec); err != nil {
			t.Fatalf("non-JSON stdout line %q: %v", sc.Text(), err)
		}
		if rec.Event == "" {
			t.Fatalf("line %q lacks event field", sc.Text())
		}
		if rec.Event == "agent_end" && rec.Reason != "done" {
			t.Errorf("agent_end reason = %q, want done", rec.Reason)
		}
		events = append(events, rec.Event)
	}
	for _, want := range []string{"agent_start", "text_delta", "message_end", "agent_end"} {
		found := false
		for _, e := range events {
			found = found || e == want
		}
		if !found {
			t.Errorf("event stream %v missing %q", events, want)
		}
	}
}

// TestE2EFollowUpMessagesAndStats chains --message follow-ups and checks
// --print-stats lands a usage summary on stderr, not stdout.
func TestE2EFollowUpMessagesAndStats(t *testing.T) {
	script := fauxScript(t, textTurn("first answer"), textTurn("second answer"))
	code, stdout, stderr := runMixi(t, script, t.TempDir(), "",
		"-p", "hi", "--message", "and again",
		"--model", "faux/scripted", "--no-save", "--print-stats")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", code, stderr)
	}
	if !strings.Contains(stdout, "second answer") {
		t.Errorf("stdout = %q, want final follow-up answer", stdout)
	}
	if !strings.Contains(stderr, "turns=2") {
		t.Errorf("stderr = %q, want turns=2 stats line", stderr)
	}
	if strings.Contains(stdout, "turns=") {
		t.Errorf("stats leaked to stdout: %q", stdout)
	}
}

// TestE2EStdinPipeImpliesPrintMode pipes a prompt with no -p flag.
func TestE2EStdinPipeImpliesPrintMode(t *testing.T) {
	script := fauxScript(t, textTurn("piped ok"))
	code, stdout, stderr := runMixi(t, script, t.TempDir(), "what is this?",
		"--model", "faux/scripted", "--no-save")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", code, stderr)
	}
	if !strings.Contains(stdout, "piped ok") {
		t.Errorf("stdout = %q, want piped-mode answer", stdout)
	}
}

// TestE2EUsageErrorsExitTwo covers the exit-2 contract for bad flags, bad
// flag values, and unknown models.
func TestE2EUsageErrorsExitTwo(t *testing.T) {
	script := fauxScript(t, textTurn("unused"))
	cases := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{"unknown flag", []string{"--definitely-not-a-flag"}, "not defined"},
		{"bad output value", []string{"-p", "hi", "--output", "yaml"}, "--output"},
		{"unknown model", []string{"-p", "hi", "--model", "nope/nope"}, "unknown model"},
		{"missing prompt", []string{"--rpc"}, "not yet available"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, _, stderr := runMixi(t, script, t.TempDir(), "", tc.args...)
			if code != 2 {
				t.Fatalf("exit = %d, want 2 (stderr: %s)", code, stderr)
			}
			if !strings.Contains(stderr, tc.wantErr) {
				t.Errorf("stderr = %q, want substring %q", stderr, tc.wantErr)
			}
		})
	}
}

// TestE2ESIGINTAbortsRunExit130 interrupts a stream that blocks on ctx:
// the run must abort with exit 130 and the session file must survive with
// the already-persisted user message.
func TestE2ESIGINTAbortsRunExit130(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no SIGINT delivery on windows")
	}
	work, sess := t.TempDir(), t.TempDir()
	script := fauxScript(t, map[string]any{"waitCtx": true})
	cmd := mixiCmd(t, script, work,
		"-p", "hang forever", "--model", "faux/scripted", "--session-dir", sess)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	// The user message is appended before the provider stream starts; once
	// the session file exists the run is inside the blocked turn.
	deadline := time.Now().Add(5 * time.Second)
	for len(sessionFiles(t, sess)) == 0 {
		if time.Now().After(deadline) {
			t.Fatalf("session file never appeared (stderr: %s)", stderr.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := cmd.Process.Signal(syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	err := cmd.Wait()
	if code := cmd.ProcessState.ExitCode(); code != 130 {
		t.Fatalf("exit = %d (err %v), want 130 (stderr: %s)", code, err, stderr.String())
	}
	files := sessionFiles(t, sess)
	if len(files) != 1 {
		t.Fatalf("session files = %v, want 1 intact session", files)
	}
	raw, err := os.ReadFile(files[0])
	if err != nil || !strings.Contains(string(raw), "hang forever") {
		t.Errorf("session lost the user message: %v, %q", err, raw)
	}
}
