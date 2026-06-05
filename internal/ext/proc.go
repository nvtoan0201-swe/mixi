package ext

import (
	"bufio"
	"encoding/json"
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

// closeGrace is the wait between shutdown escalation steps: shutdown
// message → grace → SIGKILL group.
const closeGrace = 3 * time.Second

// procLine is one pumped stdout line or its terminal error.
type procLine struct {
	msg json.RawMessage
	err error
}

// proc runs one extension as a child process speaking JSONL over its
// stdin/stdout. stderr is logged at DEBUG.
type proc struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	w      *wire.Writer
	lines  chan procLine
	exited chan struct{} // closed after cmd.Wait returns

	closeOnce sync.Once
}

// spawn starts the extension process and its pump goroutines. writeTimeout
// bounds each stdin write so a stuck extension cannot block the host.
func spawn(name string, cfg Config, log *slog.Logger, writeTimeout time.Duration) (*proc, error) {
	cmd := exec.Command(cfg.Command, cfg.Args...)
	cmd.Env = os.Environ()
	for k, v := range cfg.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	procgroup.Set(cmd)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("ext %s: stdin: %w", name, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("ext %s: stdout: %w", name, err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("ext %s: stderr: %w", name, err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("ext %s: spawn %s: %w", name, cfg.Command, err)
	}

	w := wire.NewWriter(stdin)
	if writeTimeout <= 0 {
		writeTimeout = blockingTimeout
	}
	w.SetTimeout(writeTimeout)
	p := &proc{
		cmd:    cmd,
		stdin:  stdin,
		w:      w,
		lines:  make(chan procLine, 64),
		exited: make(chan struct{}),
	}
	go p.pumpStdout(stdout)
	go pumpStderr(stderr, log, "ext."+name+".stderr")
	go func() {
		cmd.Wait()
		close(p.exited)
	}()
	return p, nil
}

// pumpStdout forwards JSONL lines. An over-long line is delivered as its
// error and reading continues (wire discards to the next boundary); any
// other error is terminal.
func (p *proc) pumpStdout(stdout io.Reader) {
	defer close(p.lines)
	r := wire.NewReader(stdout)
	for {
		msg, err := r.ReadLine()
		if err == wire.ErrLineTooLong {
			p.lines <- procLine{err: err}
			continue
		}
		if err != nil {
			p.lines <- procLine{err: err}
			return
		}
		p.lines <- procLine{msg: msg}
	}
}

// pumpStderr logs each extension stderr line at DEBUG under the given tag.
func pumpStderr(stderr io.Reader, log *slog.Logger, tag string) {
	sc := bufio.NewScanner(stderr)
	sc.Buffer(make([]byte, 0, 64<<10), wire.MaxLineBytes)
	for sc.Scan() {
		log.Debug(tag, "line", sc.Text())
	}
}

// send marshals and writes one message as a single JSONL line.
func (p *proc) send(msg any) error {
	b, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	return p.w.WriteLine(b)
}

// kill terminates the process tree with escalation: shutdown message →
// close stdin → grace → SIGKILL group. Idempotent.
func (p *proc) kill(reason string) {
	p.closeOnce.Do(func() {
		p.send(Shutdown{Type: TypeShutdown, Reason: reason}) // best effort
		p.stdin.Close()
		select {
		case <-p.exited:
			return
		case <-time.After(closeGrace):
		}
		procgroup.Kill(p.cmd, syscall.SIGKILL)
		<-p.exited
	})
}
