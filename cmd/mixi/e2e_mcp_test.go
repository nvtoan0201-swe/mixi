package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// buildFakeMCPServer compiles the mcp package's scriptable test server once
// per test process; tests skip when no Go toolchain is available.
var fakeMCPServerBin struct {
	once bool
	path string
}

func fakeMCPServer(t *testing.T) string {
	t.Helper()
	if fakeMCPServerBin.once {
		if fakeMCPServerBin.path == "" {
			t.Skip("fake MCP server unavailable (no Go toolchain)")
		}
		return fakeMCPServerBin.path
	}
	fakeMCPServerBin.once = true
	gobin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go toolchain not on PATH — cannot build fake MCP server")
	}
	dir, err := os.MkdirTemp("", "mixi-fake-mcp")
	if err != nil {
		t.Fatal(err)
	}
	// Note: shared across tests in this process; cleaned by the OS tempdir
	// policy rather than t.Cleanup so parallel tests can't race removal.
	bin := filepath.Join(dir, "fake_mcp_server")
	cmd := exec.Command(gobin, "build", "-o", bin, "fake_mcp_server.go")
	cmd.Dir = filepath.Join("..", "..", "internal", "mcp", "testdata")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fake MCP server: %v\n%s", err, out)
	}
	fakeMCPServerBin.path = bin
	return bin
}

// writeMCPSettings points the project settings at the fake server, plus any
// extra top-level settings JSON fragments.
func writeMCPSettings(t *testing.T, work, bin, extra string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(work, ".mixi"), 0o755); err != nil {
		t.Fatal(err)
	}
	settings := fmt.Sprintf(`{"mcpServers": {"fake": {"command": %q, "timeoutMs": 10000}}%s}`, bin, extra)
	if err := os.WriteFile(filepath.Join(work, ".mixi", "settings.json"), []byte(settings), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestE2EMCPToolCallEndToEnd: settings-configured server boots, its tools
// register under the namespaced name, a scripted turn calls one, and the
// MCP result lands in the session transcript.
func TestE2EMCPToolCallEndToEnd(t *testing.T) {
	work, sess := t.TempDir(), t.TempDir()
	writeMCPSettings(t, work, fakeMCPServer(t), "")
	script := fauxScript(t,
		toolCallTurn("call-1", "mcp__fake__echo", map[string]any{"msg": "ping"}),
		textTurn("mcp ok"),
	)
	code, stdout, stderr := runMixi(t, script, work, "",
		"-p", "call the echo tool", "--model", "faux/scripted",
		"--permission-mode", "yolo", "--session-dir", sess)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", code, stderr)
	}
	if !strings.Contains(stdout, "mcp ok") {
		t.Errorf("stdout = %q, want final text", stdout)
	}
	raw, err := os.ReadFile(sessionFiles(t, sess)[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "echo: ping") {
		t.Error("session transcript lacks the MCP tool result")
	}
}

// TestE2EMCPAskDeniedHeadless: prompt mode (no interactive asker) resolves
// the mcp-category ASK to a deny with an actionable reason — the gate fires
// for MCP tools exactly as for built-ins.
func TestE2EMCPAskDeniedHeadless(t *testing.T) {
	work := t.TempDir()
	writeMCPSettings(t, work, fakeMCPServer(t), "")
	script := fauxScript(t,
		toolCallTurn("call-1", "mcp__fake__echo", map[string]any{"msg": "ping"}),
		textTurn("done"),
	)
	code, stdout, stderr := runMixi(t, script, work, "",
		"-p", "call echo", "--model", "faux/scripted", "--no-save", "--output", "json")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", code, stderr)
	}
	if !strings.Contains(stdout, "Permission denied") {
		t.Fatalf("mcp call must be denied in headless prompt mode:\n%s", stdout)
	}
	if strings.Contains(stdout, "echo: ping") {
		t.Fatal("mcp tool executed despite denial")
	}
}

// TestE2EMCPAllowRuleScopesAccess: an allow rule for one namespaced tool
// admits it in prompt mode while other MCP tools still hit ASK→deny.
func TestE2EMCPAllowRuleScopesAccess(t *testing.T) {
	work := t.TempDir()
	writeMCPSettings(t, work, fakeMCPServer(t),
		`, "permissions": {"allow": ["mcp__fake__echo"]}`)
	script := fauxScript(t,
		toolCallTurn("call-1", "mcp__fake__echo", map[string]any{"msg": "ping"}),
		textTurn("done"),
	)
	code, stdout, stderr := runMixi(t, script, work, "",
		"-p", "call echo", "--model", "faux/scripted", "--no-save", "--output", "json")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", code, stderr)
	}
	if !strings.Contains(stdout, "echo: ping") {
		t.Fatalf("allowed mcp tool must execute:\n%s", stdout)
	}
}

// TestE2ENoMCPFlagSkipsServers: --no-mcp leaves the namespaced tool
// unregistered, so the scripted call resolves to an unknown-tool error.
func TestE2ENoMCPFlagSkipsServers(t *testing.T) {
	work := t.TempDir()
	writeMCPSettings(t, work, fakeMCPServer(t), "")
	script := fauxScript(t,
		toolCallTurn("call-1", "mcp__fake__echo", map[string]any{"msg": "ping"}),
		textTurn("done"),
	)
	code, stdout, stderr := runMixi(t, script, work, "",
		"-p", "call echo", "--model", "faux/scripted", "--no-save", "--no-mcp",
		"--permission-mode", "yolo", "--output", "json")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr: %s)", code, stderr)
	}
	if strings.Contains(stdout, "echo: ping") {
		t.Fatal("--no-mcp must not start MCP servers")
	}
	var sawUnknown bool
	for _, line := range strings.Split(stdout, "\n") {
		var rec struct {
			Text    string `json:"text"`
			IsError bool   `json:"isError"`
		}
		if json.Unmarshal([]byte(line), &rec) == nil &&
			rec.IsError && strings.Contains(rec.Text, "mcp__fake__echo") {
			sawUnknown = true
		}
	}
	if !sawUnknown && !strings.Contains(stdout, "Unknown tool") && !strings.Contains(stdout, "unknown tool") {
		t.Fatalf("expected unknown-tool error result:\n%s", stdout)
	}
}
