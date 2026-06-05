//go:build unix

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/user/mixi-agent/internal/ai"
	"github.com/user/mixi-agent/internal/tools"
)

// fakeServerBin is the compiled scriptable MCP server; empty when the Go
// toolchain is unavailable at test time (chaos tests then skip).
var fakeServerBin string

func TestMain(m *testing.M) {
	os.Exit(buildFakeServerAndRun(m))
}

func buildFakeServerAndRun(m *testing.M) int {
	if gobin, err := exec.LookPath("go"); err == nil {
		dir, err := os.MkdirTemp("", "mcp-fake-server")
		if err == nil {
			defer os.RemoveAll(dir)
			bin := filepath.Join(dir, "fake_mcp_server")
			cmd := exec.Command(gobin, "build", "-o", bin, "fake_mcp_server.go")
			cmd.Dir = "testdata"
			if out, err := cmd.CombinedOutput(); err == nil {
				fakeServerBin = bin
			} else {
				fmt.Fprintf(os.Stderr, "mcp: fake server build failed (chaos tests skip): %v\n%s\n", err, out)
			}
		}
	}
	return m.Run()
}

// startRealManager supervises the compiled fake server end to end.
func startRealManager(t *testing.T, env map[string]string) (*Manager, *tools.Registry) {
	t.Helper()
	if fakeServerBin == "" {
		t.Skip("go toolchain unavailable — fake MCP server not built")
	}
	reg := tools.NewRegistry()
	m, err := NewManager(ManagerOptions{
		Servers:  map[string]ServerConfig{"fake": {Command: fakeServerBin, Env: env, TimeoutMs: 5000}},
		Registry: reg,
		Backoff:  fastBackoff,
	})
	if err != nil {
		t.Fatal(err)
	}
	m.Start(context.Background())
	t.Cleanup(m.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	m.WaitInitial(ctx)
	return m, reg
}

func TestChaosCrashMidCallThenRestartThenSuccess(t *testing.T) {
	m, reg := startRealManager(t, nil)
	waitState(t, m, "fake", StateReady)

	echo, ok := reg.Get("mcp__fake__echo")
	if !ok {
		t.Fatalf("echo not adapted; status %+v", m.Status())
	}
	r, err := echo.Execute(context.Background(), json.RawMessage(`{"msg":"hi"}`), nil)
	if err != nil || r.IsError {
		t.Fatalf("first echo: %v %+v", err, r)
	}
	if txt := r.Content[0].(ai.TextContent).Text; txt != "echo: hi" {
		t.Fatalf("echo content = %q", txt)
	}

	// The crash tool exits the server process without responding — the
	// in-flight call must resolve to the LLM-visible crash result.
	crash, _ := reg.Get("mcp__fake__crash")
	r, err = crash.Execute(context.Background(), json.RawMessage(`{}`), nil)
	if err != nil {
		t.Fatalf("crash call returned hard error: %v", err)
	}
	if !r.IsError || !strings.Contains(r.Content[0].(ai.TextContent).Text, "crashed during call") {
		t.Fatalf("crash result = %+v", r)
	}

	// The supervisor restarts the process; the same registered tool works
	// again. Retry through the restart window (unavailable results are
	// expected while the server is down — exactly what the LLM would see).
	deadline := time.Now().Add(10 * time.Second)
	for {
		r, err = echo.Execute(context.Background(), json.RawMessage(`{"msg":"again"}`), nil)
		if err == nil && !r.IsError {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("echo never recovered after crash: %v %+v", err, r)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if txt := r.Content[0].(ai.TextContent).Text; txt != "echo: again" {
		t.Fatalf("post-restart content = %q", txt)
	}
}

func TestChaosOldProtocolFallback(t *testing.T) {
	m, _ := startRealManager(t, map[string]string{"MIXI_FAKE_MCP_VERSION": FallbackProtocolVersion})
	waitState(t, m, "fake", StateReady)
}

func TestChaosExitOnStartThreeStrikes(t *testing.T) {
	m, _ := startRealManager(t, map[string]string{"MIXI_FAKE_MCP_EXIT_ON_START": "1"})
	waitState(t, m, "fake", StateFailed)
}
