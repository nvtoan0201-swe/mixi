//go:build unix

package ext

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/user/mixi-agent/internal/agent"
	"github.com/user/mixi-agent/internal/ai"
)

func TestExtensionToolRoundTrip(t *testing.T) {
	h, reg := startTestHost(t, "tool", hostOpts{})
	waitExtState(t, h, "fake", StateReady)

	tool, ok := reg.Get("fake_tool")
	if !ok {
		t.Fatalf("fake_tool not registered; status %+v", h.Status())
	}
	if !strings.HasPrefix(tool.Description(), "[ext:fake] ") {
		t.Fatalf("description = %q", tool.Description())
	}
	res, err := tool.Execute(context.Background(), json.RawMessage(`{"x":1}`), nil)
	if err != nil || res.IsError {
		t.Fatalf("execute: %v %+v", err, res)
	}
	text := res.Content[0].(ai.TextContent).Text
	if !strings.Contains(text, `echo:{"x":1}`) {
		t.Fatalf("result text = %q", text)
	}
	if st := h.Status(); st[0].Tools != 1 {
		t.Fatalf("status tools = %+v", st)
	}
}

func TestToolNameCollisionFirstRegisteredWins(t *testing.T) {
	if fakeExtBin == "" {
		t.Skip("go toolchain unavailable")
	}
	mk := func() Config {
		return Config{Command: fakeExtBin, Env: map[string]string{
			"FAKE_EXT_MODE": "tool", "FAKE_EXT_TOOL": "shared_tool"}}
	}
	h, reg := startTestHost(t, "", hostOpts{servers: map[string]Config{"a": mk(), "b": mk()}})
	waitExtState(t, h, "a", StateReady)
	waitExtState(t, h, "b", StateReady)

	if _, ok := reg.Get("shared_tool"); !ok {
		t.Fatal("shared_tool missing")
	}
	st := h.Status() // deterministic order: a, b — a registered first
	if st[0].Tools+st[1].Tools != 1 {
		t.Fatalf("exactly one adoption expected: %+v", st)
	}
	if st[0].Name == "a" && st[0].Tools != 1 {
		t.Fatalf("first-registered must win: %+v", st)
	}
}

func TestActionsReachAPI(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "ext.log")
	h, _ := startTestHost(t, "actions", hostOpts{env: map[string]string{"FAKE_EXT_LOG": logPath}})
	waitExtState(t, h, "fake", StateReady)
	// The fake fires its actions on ready, before Bind: this also covers
	// the pre-bind buffering path.
	api, _ := bindRecorder(t, h)

	waitFor(t, "all actions to land", func() bool {
		api.mu.Lock()
		defer api.mu.Unlock()
		return api.statuses["fake"] == "hi" &&
			len(api.notices) == 1 && len(api.messages) == 1 && len(api.entries) == 1
	})
	api.mu.Lock()
	defer api.mu.Unlock()
	if api.messages[0] != "steer:steer me" {
		t.Fatalf("messages = %v", api.messages)
	}
	if !strings.HasPrefix(api.entries[0], "fake.note:") {
		t.Fatalf("entries = %v", api.entries)
	}
	// ask_select got the first option; register_tool was rejected.
	waitFor(t, "action responses", func() bool {
		b, _ := os.ReadFile(logPath)
		return strings.Contains(string(b), `"choice":"a"`) &&
			strings.Contains(string(b), "register_tool is hello-only")
	})
}

func TestLifecycleEventOrder(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "ext.log")
	h, _ := startTestHost(t, "echo", hostOpts{env: map[string]string{"FAKE_EXT_LOG": logPath}})
	waitExtState(t, h, "fake", StateReady)
	_, events := bindRecorder(t, h)

	events <- agent.EvAgentStart{}
	events <- agent.EvTurnStart{Turn: 1}
	events <- agent.EvTurnEnd{Turn: 1}
	events <- agent.EvAgentEnd{Reason: agent.EndDone}
	waitFor(t, "agent events to flush", func() bool {
		b, _ := os.ReadFile(logPath)
		return strings.Contains(string(b), "agent_end")
	})
	h.Close()

	b, _ := os.ReadFile(logPath)
	var names []string
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var m struct {
			Type  string `json:"type"`
			Event string `json:"event"`
		}
		if json.Unmarshal([]byte(line), &m) != nil {
			continue
		}
		switch m.Type {
		case "ready":
			names = append(names, "ready")
		case "event":
			names = append(names, m.Event)
		}
	}
	want := []string{"ready", "session_start", "agent_start", "turn_start", "turn_end", "agent_end", "session_shutdown"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("order = %v, want %v", names, want)
	}
}

func TestDispatchCommandDeliversUserInput(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "ext.log")
	h, _ := startTestHost(t, "tool", hostOpts{env: map[string]string{"FAKE_EXT_LOG": logPath}})
	waitExtState(t, h, "fake", StateReady)
	bindRecorder(t, h)

	cmds := h.Commands()
	if len(cmds) != 1 || cmds[0].Name != "fakecmd" {
		t.Fatalf("commands = %+v", cmds)
	}
	if err := h.DispatchCommand("fakecmd", "arg1"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "user_input delivery", func() bool {
		b, _ := os.ReadFile(logPath)
		return strings.Contains(string(b), `"/fakecmd arg1"`)
	})
	if err := h.DispatchCommand("nope", ""); err == nil {
		t.Fatal("unknown command must error")
	}
}
