//go:build unix

package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestStdioRoundTripAndOrderlyClose(t *testing.T) {
	tr, err := NewStdio(StdioOptions{
		Name:    "echo",
		Command: "sh",
		Args:    []string{"-c", `while IFS= read -r l; do printf '%s\n' "$l"; done`},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	msg := json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"ping"}`)
	if err := tr.Send(ctx, msg); err != nil {
		t.Fatal(err)
	}
	got, err := tr.Receive(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(msg) {
		t.Fatalf("echo = %s, want %s", got, msg)
	}

	done := make(chan error, 1)
	go func() { done <- tr.Close() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("close: %v", err)
		}
	case <-time.After(closeGrace + time.Second):
		t.Fatal("orderly close did not finish before first escalation")
	}
	// After close the stream must drain to EOF (the echo child reflects the
	// shutdown notification back first).
	for i := 0; ; i++ {
		if _, err := tr.Receive(ctx); err == io.EOF {
			break
		} else if err != nil {
			t.Fatalf("post-close receive err = %v, want EOF", err)
		}
		if i > 2 {
			t.Fatal("stream did not reach EOF after close")
		}
	}
}

func TestStdioCloseEscalatesToKill(t *testing.T) {
	// The child ignores SIGTERM and never reads stdin, so Close must walk
	// the full escalation: stdin close → SIGTERM → SIGKILL.
	tr, err := NewStdio(StdioOptions{
		Name:    "stubborn",
		Command: "sh",
		Args:    []string{"-c", `trap '' TERM; while :; do sleep 0.2; done`},
	})
	if err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	done := make(chan error, 1)
	go func() { done <- tr.Close() }()
	select {
	case <-done:
	case <-time.After(3*closeGrace + 2*time.Second):
		t.Fatal("Close never returned — SIGKILL escalation failed")
	}
	if elapsed := time.Since(start); elapsed < 2*closeGrace {
		t.Logf("close took %v (child exited before full escalation?)", elapsed)
	}
}

// memLogHandler captures slog output for assertions.
type memLog struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (m *memLog) Write(p []byte) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.buf.Write(p)
}

func (m *memLog) String() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.buf.String()
}

func TestStdioStderrLoggedAtDebug(t *testing.T) {
	var ml memLog
	log := slog.New(slog.NewTextHandler(&ml, &slog.HandlerOptions{Level: slog.LevelDebug}))
	tr, err := NewStdio(StdioOptions{
		Name:    "noisy",
		Command: "sh",
		Args:    []string{"-c", `echo "boot warning" >&2; while IFS= read -r l; do :; done`},
		Log:     log,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if s := ml.String(); strings.Contains(s, "mcp.noisy.stderr") && strings.Contains(s, "boot warning") {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("stderr not logged; log:\n%s", ml.String())
}

func TestStdioSpawnError(t *testing.T) {
	if _, err := NewStdio(StdioOptions{Name: "ghost", Command: "/nonexistent/binary"}); err == nil {
		t.Fatal("spawn of missing binary must error")
	}
}

func TestStdioEnvOverlay(t *testing.T) {
	tr, err := NewStdio(StdioOptions{
		Name:    "env",
		Command: "sh",
		Args:    []string{"-c", `printf '{"v":"%s"}\n' "$MIXI_MCP_TEST_VAR"`},
		Env:     map[string]string{"MIXI_MCP_TEST_VAR": "hello"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	got, err := tr.Receive(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"v":"hello"}` {
		t.Fatalf("env overlay = %s", got)
	}
}
