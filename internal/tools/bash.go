package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

type bashTool struct {
	cwd  string
	jobs *JobTable
}

func (t *bashTool) Name() string { return "bash" }

func (t *bashTool) Description() string {
	return "Execute a shell command. Streams output; stdout+stderr interleaved. " +
		"Default timeout 120s (override with timeout, max 600). " +
		"Set background=true for long-running processes and read output with bash_output."
}

func (t *bashTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{
"command":{"type":"string","description":"Shell command to run"},
"timeout":{"type":"integer","minimum":1,"maximum":600,"description":"Seconds"},
"background":{"type":"boolean","description":"Run detached; returns a job id"}
},"required":["command"]}`)
}

// Mode is sequential: any batch containing bash runs one call at a time.
func (t *bashTool) Mode() ExecMode { return ExecSequential }

func (t *bashTool) Execute(ctx context.Context, args json.RawMessage, updates chan<- ToolUpdate) (ToolResult, error) {
	var a struct {
		Command    string `json:"command"`
		Timeout    int    `json:"timeout"`
		Background bool   `json:"background"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return ToolResult{}, fmt.Errorf("bash: bad arguments: %w", err)
	}
	timeout := BashDefaultTimeout
	if a.Timeout > 0 {
		timeout = min(time.Duration(a.Timeout)*time.Second, BashMaxTimeout)
	}

	cmd := exec.Command(shellPath(), "-c", a.Command)
	cmd.Dir = t.cwd
	setProcGroup(cmd)
	acc := newAccumulator()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return ToolResult{}, fmt.Errorf("bash: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return ToolResult{}, fmt.Errorf("bash: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return Errorf("bash: %v", err), nil
	}
	var pipes sync.WaitGroup
	pipes.Add(2)
	go func() { defer pipes.Done(); io.Copy(acc, stdout) }()
	go func() { defer pipes.Done(); io.Copy(acc, stderr) }()

	if a.Background {
		job := t.jobs.add(cmd, acc, &pipes)
		return Text(fmt.Sprintf("started background job %s (pid %d)", job.ID, cmd.Process.Pid)), nil
	}
	return t.runForeground(ctx, cmd, acc, &pipes, timeout, updates)
}

// runForeground waits for completion, streaming throttled output snapshots
// and enforcing timeout/abort via group SIGTERM→2s→SIGKILL.
func (t *bashTool) runForeground(ctx context.Context, cmd *exec.Cmd, acc *accumulator,
	pipes *sync.WaitGroup, timeout time.Duration, updates chan<- ToolUpdate) (ToolResult, error) {

	exited := make(chan struct{})
	exitCh := make(chan int, 1)
	go func() {
		pipes.Wait()
		err := cmd.Wait()
		close(exited)
		exitCh <- commandExitCode(err)
	}()

	ticker := time.NewTicker(BashUpdateThrottle)
	defer ticker.Stop()
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	var timedOut, aborted bool
	exit := 0
	ctxDone := ctx.Done() // nil-ed after first fire so the closed chan can't spin the loop
wait:
	for {
		select {
		case exit = <-exitCh:
			break wait
		case <-ticker.C:
			if updates != nil {
				select {
				case updates <- ToolUpdate{Content: acc.snapshot().Tail}:
				default: // dispatcher busy; drop this snapshot
				}
			}
		case <-timer.C:
			timedOut = true
			terminateGroup(cmd, exited)
		case <-ctxDone:
			aborted = true
			terminateGroup(cmd, exited)
			ctxDone = nil
		}
	}
	return bashResult(acc, exit, timedOut, aborted, timeout), nil
}

// bashResult formats tail-truncated output plus status notes.
func bashResult(acc *accumulator, exit int, timedOut, aborted bool, timeout time.Duration) ToolResult {
	snap := acc.snapshot()
	out, truncated := tailTruncate(snap.Tail, MaxLines, MaxBytes)
	out = strings.TrimRight(out, "\n")
	acc.close()

	var notes []string
	if truncated {
		if path := acc.persistFull(); path != "" {
			notes = append(notes, fmt.Sprintf("[full output: %s]", path))
		}
	}
	if timedOut {
		notes = append(notes, fmt.Sprintf("command timed out after %ds", int(timeout.Seconds())))
	}
	if aborted {
		notes = append(notes, "command aborted")
	}
	if exit != 0 && !timedOut && !aborted {
		notes = append(notes, fmt.Sprintf("exit code %d", exit))
	}
	if out == "" {
		out = "(no output)"
	}
	if len(notes) > 0 {
		out += "\n" + strings.Join(notes, "\n")
	}
	res := Text(out)
	res.IsError = exit != 0 || timedOut || aborted
	return res
}

// shellPath honors the user's shell, matching interactive expectations.
func shellPath() string {
	if s := os.Getenv("SHELL"); s != "" {
		return s
	}
	return "/bin/bash"
}

// commandExitCode maps cmd.Wait errors to a numeric code (-1 = unknown).
func commandExitCode(err error) int {
	if err == nil {
		return 0
	}
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode()
	}
	return -1
}
