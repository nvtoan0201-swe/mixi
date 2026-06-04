//go:build unix

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
)

func newBashTool(t *testing.T) *bashTool {
	t.Helper()
	jobs := NewJobTable()
	t.Cleanup(jobs.KillAll)
	return &bashTool{cwd: t.TempDir(), jobs: jobs}
}

func TestBashEchoAndExitCode(t *testing.T) {
	b := newBashTool(t)
	res := execTool(t, b, `{"command":"echo hello"}`)
	if res.IsError || resultText(t, res) != "hello" {
		t.Fatalf("res=%+v", res)
	}
	res = execTool(t, b, `{"command":"echo boom >&2; exit 3"}`)
	out := resultText(t, res)
	if !res.IsError || !strings.Contains(out, "boom") || !strings.Contains(out, "exit code 3") {
		t.Fatalf("out=%q isError=%v", out, res.IsError)
	}
}

func TestBashInterleavesStdoutStderr(t *testing.T) {
	b := newBashTool(t)
	out := resultText(t, execTool(t, b, `{"command":"echo out1; echo err1 >&2; echo out2"}`))
	for _, want := range []string{"out1", "err1", "out2"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in %q", want, out)
		}
	}
}

func TestBashNoOutput(t *testing.T) {
	b := newBashTool(t)
	if got := resultText(t, execTool(t, b, `{"command":"true"}`)); got != "(no output)" {
		t.Fatalf("got=%q", got)
	}
}

func TestBashTimeoutKillsProcessTree(t *testing.T) {
	b := newBashTool(t)
	pidFile := b.cwd + "/child.pid"
	cmd := fmt.Sprintf(`{"command":"sleep 999 & echo $! > %s; wait","timeout":1}`, pidFile)
	start := time.Now()
	res := execTool(t, b, cmd)
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("timeout took %v", elapsed)
	}
	if !res.IsError || !strings.Contains(resultText(t, res), "command timed out after 1s") {
		t.Fatalf("res=%q", resultText(t, res))
	}
	// The grandchild sleep must die with the group — no orphans.
	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("child pid not captured: %v", err)
	}
	var pid int
	fmt.Sscanf(strings.TrimSpace(string(data)), "%d", &pid)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if syscall.Kill(pid, 0) != nil {
			return // child gone
		}
		time.Sleep(100 * time.Millisecond)
	}
	syscall.Kill(pid, syscall.SIGKILL)
	t.Fatalf("orphaned child %d survived group kill", pid)
}

func TestBashAbortViaContext(t *testing.T) {
	b := newBashTool(t)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(300 * time.Millisecond); cancel() }()
	start := time.Now()
	res, err := b.Execute(ctx, json.RawMessage(`{"command":"sleep 60"}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 10*time.Second {
		t.Fatal("abort did not interrupt promptly")
	}
	if !res.IsError || !strings.Contains(resultText(t, res), "command aborted") {
		t.Fatalf("res=%q", resultText(t, res))
	}
}

func TestBashTailTruncationWithFullOutputHint(t *testing.T) {
	b := newBashTool(t)
	res := execTool(t, b, `{"command":"seq 1 5000"}`)
	out := resultText(t, res)
	if res.IsError {
		t.Fatalf("unexpected error: %q", out)
	}
	if !strings.Contains(out, "5000") {
		t.Fatal("tail truncation must keep the END of output")
	}
	if strings.Contains(out, "\n1\n") {
		t.Fatal("head of output should have been cut")
	}
	if !strings.Contains(out, "[full output: ") {
		t.Fatalf("missing full-output hint: %q", out[len(out)-200:])
	}
}

func TestBashStreamsUpdates(t *testing.T) {
	b := newBashTool(t)
	updates := make(chan ToolUpdate, 64)
	done := make(chan struct{})
	var sawPartial bool
	go func() {
		defer close(done)
		for u := range updates {
			if strings.Contains(u.Content, "tick") {
				sawPartial = true
			}
		}
	}()
	res, err := b.Execute(context.Background(),
		json.RawMessage(`{"command":"echo tick; sleep 0.5; echo tock"}`), updates)
	close(updates)
	<-done
	if err != nil || res.IsError {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if !sawPartial {
		t.Fatal("no streamed update observed during execution")
	}
}

func TestBashSequentialMode(t *testing.T) {
	if (&bashTool{}).Mode() != ExecSequential {
		t.Fatal("bash must be ExecSequential")
	}
}
