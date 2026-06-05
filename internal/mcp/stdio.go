package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/user/mixi-agent/internal/procgroup"
	"github.com/user/mixi-agent/internal/wire"
)

// closeGrace is the wait between escalation steps on Close: stdin close →
// grace → SIGTERM → grace → SIGKILL.
const closeGrace = 2 * time.Second

// StdioOptions configures a subprocess transport.
type StdioOptions struct {
	Name    string            // server key, used in log tags
	Command string            // executable
	Args    []string          // argv tail
	Env     map[string]string // overlaid on the parent environment
	Log     *slog.Logger      // nil = default
	// WriteTimeout bounds one Send so a server that stops draining stdin
	// cannot block callers forever (0 = DefaultCallTimeout). Effective on
	// platforms where pipe write deadlines work (Linux does).
	WriteTimeout time.Duration
}

// readResult is one pumped line or its terminal error.
type readResult struct {
	msg json.RawMessage
	err error
}

// stdioTransport runs an MCP server as a child process speaking JSONL over
// its stdin/stdout. stderr is logged at DEBUG.
type stdioTransport struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	w      *wire.Writer
	lines  chan readResult
	exited chan struct{} // closed after cmd.Wait returns

	closeOnce sync.Once
	closeErr  error
}

// NewStdio spawns the server process and starts its pump goroutines.
func NewStdio(opts StdioOptions) (Transport, error) {
	log := opts.Log
	if log == nil {
		log = slog.Default()
	}
	cmd := exec.Command(opts.Command, opts.Args...)
	cmd.Env = os.Environ()
	for k, v := range opts.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	procgroup.Set(cmd)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("mcp %s: stdin: %w", opts.Name, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("mcp %s: stdout: %w", opts.Name, err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("mcp %s: stderr: %w", opts.Name, err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("mcp %s: spawn %s: %w", opts.Name, opts.Command, err)
	}

	w := wire.NewWriter(stdin)
	if opts.WriteTimeout <= 0 {
		opts.WriteTimeout = DefaultCallTimeout
	}
	w.SetTimeout(opts.WriteTimeout)

	t := &stdioTransport{
		cmd:    cmd,
		stdin:  stdin,
		w:      w,
		lines:  make(chan readResult, 16),
		exited: make(chan struct{}),
	}
	go t.pumpStdout(stdout)
	go pumpStderr(stderr, log, "mcp."+opts.Name+".stderr")
	go func() {
		cmd.Wait()
		close(t.exited)
	}()
	return t, nil
}

// pumpStdout forwards JSONL lines to the receive channel. A line-cap error
// is delivered and reading continues (wire discards the rest of the line);
// any other error is terminal.
func (t *stdioTransport) pumpStdout(stdout io.Reader) {
	defer close(t.lines)
	r := wire.NewReader(stdout)
	for {
		msg, err := r.ReadLine()
		if errors.Is(err, wire.ErrLineTooLong) {
			t.lines <- readResult{err: err}
			continue
		}
		if err != nil {
			t.lines <- readResult{err: err}
			return
		}
		t.lines <- readResult{msg: msg}
	}
}

// pumpStderr logs each server stderr line at DEBUG under the given tag.
func pumpStderr(stderr io.Reader, log *slog.Logger, tag string) {
	sc := bufio.NewScanner(stderr)
	sc.Buffer(make([]byte, 0, 64<<10), wire.MaxLineBytes)
	for sc.Scan() {
		log.Debug(tag, "line", sc.Text())
	}
}

func (t *stdioTransport) Send(ctx context.Context, msg json.RawMessage) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return t.w.WriteLine(msg)
}

func (t *stdioTransport) Receive(ctx context.Context) (json.RawMessage, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case r, ok := <-t.lines:
		if !ok {
			return nil, io.EOF
		}
		return r.msg, r.err
	}
}

// Close shuts the server down with escalation: best-effort shutdown
// notification → close stdin → grace → SIGTERM group → grace → SIGKILL.
func (t *stdioTransport) Close() error {
	t.closeOnce.Do(func() {
		if b, err := json.Marshal(Notification{JSONRPC: "2.0", Method: "shutdown"}); err == nil {
			t.w.WriteLine(b) // best effort; server may already be gone
		}
		t.stdin.Close()
		if t.waitExit(closeGrace) {
			return
		}
		procgroup.Kill(t.cmd, syscall.SIGTERM)
		if t.waitExit(closeGrace) {
			return
		}
		procgroup.Kill(t.cmd, syscall.SIGKILL)
		<-t.exited
	})
	return t.closeErr
}

// waitExit reports whether the process exited within d.
func (t *stdioTransport) waitExit(d time.Duration) bool {
	select {
	case <-t.exited:
		return true
	case <-time.After(d):
		return false
	}
}
