//go:build mcp_integration

package mcp

// Conformance smoke against the reference everything-server
// (modelcontextprotocol/servers). Local-only, never in CI:
//
//	go test -tags=mcp_integration -run TestEverythingServer ./internal/mcp/
//
// Needs node/npx on PATH; downloads the server package on first run.

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/user/mixi-agent/internal/ai"
	"github.com/user/mixi-agent/internal/tools"
)

func TestEverythingServerListAndEcho(t *testing.T) {
	if _, err := exec.LookPath("npx"); err != nil {
		t.Skip("npx not on PATH")
	}
	reg := tools.NewRegistry()
	m, err := NewManager(ManagerOptions{
		Servers: map[string]ServerConfig{"everything": {
			Command:   "npx",
			Args:      []string{"-y", "@modelcontextprotocol/server-everything", "stdio"},
			TimeoutMs: 60000, // first run downloads the package
		}},
		Registry: reg,
	})
	if err != nil {
		t.Fatal(err)
	}
	m.Start(context.Background())
	t.Cleanup(m.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	m.WaitInitial(ctx)
	waitState(t, m, "everything", StateReady)

	status := m.Status()
	if status[0].Tools == 0 {
		t.Fatal("everything-server listed no tools")
	}
	t.Logf("everything-server: %d tools", status[0].Tools)

	echo, ok := reg.Get("mcp__everything__echo")
	if !ok {
		var names []string
		for _, d := range reg.Defs() {
			names = append(names, d.Name)
		}
		t.Fatalf("echo tool missing; have %v", names)
	}
	r, err := echo.Execute(ctx, json.RawMessage(`{"message":"conformance"}`), nil)
	if err != nil || r.IsError {
		t.Fatalf("echo call: %v %+v", err, r)
	}
	if txt := r.Content[0].(ai.TextContent).Text; !strings.Contains(txt, "conformance") {
		t.Fatalf("echo content = %q", txt)
	}
}
