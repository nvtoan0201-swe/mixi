package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/user/mixi-agent/internal/ai"
	"github.com/user/mixi-agent/internal/tools"
)

// fastBackoff keeps lifecycle tests quick.
var fastBackoff = []time.Duration{10 * time.Millisecond, 10 * time.Millisecond, 10 * time.Millisecond}

// transportFactory hands out fresh chanTransports per spawn and remembers
// them so tests can crash specific generations. perSpawn overrides the
// handler for the n-th spawn (nil = use the default handler).
type transportFactory struct {
	mu       sync.Mutex
	handler  func(string, Request) (any, *RPCError)
	perSpawn []func(string, Request) (any, *RPCError)
	spawned  []*chanTransport
	t        *testing.T
}

func (f *transportFactory) new(name string, cfg ServerConfig) (Transport, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	tr := newChanTransport()
	h := f.handler
	if n := len(f.spawned); n < len(f.perSpawn) && f.perSpawn[n] != nil {
		h = f.perSpawn[n]
	}
	tr.serve(f.t, nil, nil, h)
	f.spawned = append(f.spawned, tr)
	return tr, nil
}

func (f *transportFactory) latest() *chanTransport {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.spawned[len(f.spawned)-1]
}

func (f *transportFactory) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.spawned)
}

// startManager builds a manager over the factory and waits for the initial
// settle.
func startManager(t *testing.T, servers map[string]ServerConfig, f *transportFactory,
	notify func(string)) (*Manager, *tools.Registry) {
	t.Helper()
	reg := tools.NewRegistry()
	m, err := NewManager(ManagerOptions{
		Servers: servers, Registry: reg, Notify: notify,
		NewTransport: f.new, Backoff: fastBackoff, StableAfter: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	m.Start(context.Background())
	t.Cleanup(m.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	m.WaitInitial(ctx)
	return m, reg
}

func waitState(t *testing.T, m *Manager, server string, want State) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, st := range m.Status() {
			if st.Name == server && st.State == want {
				return
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("server %q never reached %s; status=%+v", server, want, m.Status())
}

func TestManagerReadyRegistersNamespacedTools(t *testing.T) {
	f := &transportFactory{t: t, handler: stdHandler(ProtocolVersion)}
	m, reg := startManager(t, map[string]ServerConfig{"git hub!": {Command: "x"}}, f, nil)

	waitState(t, m, "git hub!", StateReady)
	tool, ok := reg.Get("mcp__git_hub___echo")
	if !ok {
		t.Fatalf("namespaced tool not registered; defs=%+v", reg.Defs())
	}
	if d := tool.Description(); !strings.HasPrefix(d, "[mcp:git hub!] ") {
		t.Fatalf("description prefix missing: %q", d)
	}
	if !json.Valid(tool.Schema()) || !strings.Contains(string(tool.Schema()), `"object"`) {
		t.Fatalf("schema passthrough broken: %s", tool.Schema())
	}
	// Anthropic-safe charset on the registered name.
	if strings.ContainsAny(tool.Name(), " !") {
		t.Fatalf("unsanitized tool name %q", tool.Name())
	}
}

func TestManagerCrashMidCallRestartSecondCallSucceeds(t *testing.T) {
	// First spawn never answers tools/call (the call stays pending until
	// the crash); the restart spawn behaves normally.
	hang := func(method string, req Request) (any, *RPCError) {
		if method == "tools/call" {
			select {} // never respond — the crash interrupts
		}
		return stdHandler(ProtocolVersion)(method, req)
	}
	f := &transportFactory{t: t, handler: stdHandler(ProtocolVersion),
		perSpawn: []func(string, Request) (any, *RPCError){hang}}
	m, reg := startManager(t, map[string]ServerConfig{"srv": {Command: "x"}}, f, nil)
	waitState(t, m, "srv", StateReady)
	tool, _ := reg.Get("mcp__srv__echo")

	first := f.latest()
	resCh := make(chan tools.ToolResult, 1)
	go func() {
		r, err := tool.Execute(context.Background(), json.RawMessage(`{"msg":"a"}`), nil)
		if err != nil {
			r = tools.Errorf("unexpected error: %v", err)
		}
		resCh <- r
	}()
	time.Sleep(30 * time.Millisecond) // let the call go pending
	first.Close()                     // crash mid-call

	select {
	case r := <-resCh:
		if !r.IsError {
			t.Fatalf("crash mid-call must produce an error result: %+v", r)
		}
		txt := r.Content[0].(ai.TextContent).Text
		if !strings.Contains(txt, "crashed during call") || !strings.Contains(txt, "retry") {
			t.Fatalf("crash text = %q", txt)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("mid-call crash never resolved the tool result")
	}

	// Manager restarts; the same registered tool works again. Wait for the
	// respawn first — the old READY state lingers until the supervisor
	// observes the dead connection.
	deadline := time.Now().Add(5 * time.Second)
	for f.count() < 2 {
		if time.Now().After(deadline) {
			t.Fatal("no restart spawn observed")
		}
		time.Sleep(5 * time.Millisecond)
	}
	waitState(t, m, "srv", StateReady)
	r, err := tool.Execute(context.Background(), json.RawMessage(`{"msg":"b"}`), nil)
	if err != nil || r.IsError {
		t.Fatalf("second call after restart: %v %+v", err, r)
	}
}

func TestManagerThreeStrikesDisablesAndReconnectRevives(t *testing.T) {
	// Transports that immediately EOF: every handshake fails.
	broken := true
	var mu sync.Mutex
	var notices []string
	f := &transportFactory{t: t, handler: stdHandler(ProtocolVersion)}
	factory := func(name string, cfg ServerConfig) (Transport, error) {
		tr, _ := f.new(name, cfg)
		mu.Lock()
		b := broken
		mu.Unlock()
		if b {
			tr.Close() // instant EOF → handshake failure
		}
		return tr, nil
	}
	reg := tools.NewRegistry()
	m, err := NewManager(ManagerOptions{
		Servers:  map[string]ServerConfig{"flaky": {Command: "x", TimeoutMs: 200}},
		Registry: reg,
		Notify: func(s string) {
			mu.Lock()
			notices = append(notices, s)
			mu.Unlock()
		},
		NewTransport: factory, Backoff: fastBackoff, StableAfter: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	m.Start(context.Background())
	t.Cleanup(m.Close)

	waitState(t, m, "flaky", StateFailed)
	mu.Lock()
	gotNotice := len(notices) > 0 && strings.Contains(notices[0], "disabled")
	mu.Unlock()
	if !gotNotice {
		t.Fatalf("FAILED must notify the user; notices=%v", notices)
	}

	// While failed, adapted calls (if any tool had registered) would error;
	// reconnect with a healthy factory revives the server.
	mu.Lock()
	broken = false
	mu.Unlock()
	if err := m.Reconnect("flaky"); err != nil {
		t.Fatal(err)
	}
	waitState(t, m, "flaky", StateReady)
	if err := m.Reconnect("flaky"); err == nil {
		t.Fatal("reconnect of a ready server must error")
	}
}

func TestManagerSpawnErrorFailsImmediately(t *testing.T) {
	reg := tools.NewRegistry()
	m, err := NewManager(ManagerOptions{
		Servers:  map[string]ServerConfig{"ghost": {Command: "/nonexistent"}},
		Registry: reg,
		NewTransport: func(string, ServerConfig) (Transport, error) {
			return nil, context.DeadlineExceeded
		},
		Backoff: fastBackoff,
	})
	if err != nil {
		t.Fatal(err)
	}
	m.Start(context.Background())
	t.Cleanup(m.Close)
	waitState(t, m, "ghost", StateFailed)
}

func TestManagerCollisionByConstruction(t *testing.T) {
	// Two servers offering the same tool name must register distinct,
	// namespaced tools.
	f := &transportFactory{t: t, handler: stdHandler(ProtocolVersion)}
	m, reg := startManager(t, map[string]ServerConfig{
		"alpha": {Command: "x"}, "beta": {Command: "x"},
	}, f, nil)
	waitState(t, m, "alpha", StateReady)
	waitState(t, m, "beta", StateReady)

	if _, ok := reg.Get("mcp__alpha__echo"); !ok {
		t.Fatal("mcp__alpha__echo missing")
	}
	if _, ok := reg.Get("mcp__beta__echo"); !ok {
		t.Fatal("mcp__beta__echo missing")
	}
}

func TestManagerSanitizedServerKeyCollisionRejected(t *testing.T) {
	_, err := NewManager(ManagerOptions{Servers: map[string]ServerConfig{
		"my srv": {Command: "x"}, "my_srv": {Command: "x"},
	}, Registry: tools.NewRegistry()})
	if err == nil || !strings.Contains(err.Error(), "collide") {
		t.Fatalf("sanitized collision must be a config error, got %v", err)
	}
}

func TestManagerUnavailableAndRemovedTools(t *testing.T) {
	f := &transportFactory{t: t, handler: stdHandler(ProtocolVersion)}
	m, reg := startManager(t, map[string]ServerConfig{"srv": {Command: "x"}}, f, nil)
	waitState(t, m, "srv", StateReady)
	tool, _ := reg.Get("mcp__srv__echo")

	// Simulate a re-list that drops the tool: it stays registered but
	// errors when called.
	s := m.servers["srv"]
	m.adoptTools(s, nil)
	r, err := tool.Execute(context.Background(), nil, nil)
	if err != nil || !r.IsError {
		t.Fatalf("removed tool call: %v %+v", err, r)
	}
	if txt := r.Content[0].(ai.TextContent).Text; !strings.Contains(txt, "no longer provided") {
		t.Fatalf("removed-tool text = %q", txt)
	}
	if _, ok := reg.Get("mcp__srv__echo"); !ok {
		t.Fatal("removed tool must stay registered (no schema churn)")
	}
	// Schema/description still served from last-known info.
	if !strings.Contains(tool.Description(), "[mcp:srv] ") {
		t.Fatalf("last-known description lost: %q", tool.Description())
	}

	// Unavailable state (restarting/failed): calls error with retry hint.
	s.setState(StateRestarting)
	r, err = tool.Execute(context.Background(), nil, nil)
	if err != nil || !r.IsError {
		t.Fatalf("unavailable call: %v %+v", err, r)
	}
	if txt := r.Content[0].(ai.TextContent).Text; !strings.Contains(txt, "temporarily unavailable") {
		t.Fatalf("unavailable text = %q", txt)
	}
}

func TestManagerListChangedRegistersNewTool(t *testing.T) {
	var mu sync.Mutex
	extra := false
	handler := func(method string, req Request) (any, *RPCError) {
		if method == "tools/list" {
			mu.Lock()
			withExtra := extra
			mu.Unlock()
			tools := []ToolInfo{{Name: "echo", Description: "echoes",
				InputSchema: json.RawMessage(`{"type":"object"}`)}}
			if withExtra {
				tools = append(tools, ToolInfo{Name: "fresh", Description: "new arrival",
					InputSchema: json.RawMessage(`{"type":"object"}`)})
			}
			return listToolsResult{Tools: tools}, nil
		}
		return stdHandler(ProtocolVersion)(method, req)
	}
	f := &transportFactory{t: t, handler: handler}
	m, reg := startManager(t, map[string]ServerConfig{"srv": {Command: "x"}}, f, nil)
	waitState(t, m, "srv", StateReady)

	mu.Lock()
	extra = true
	mu.Unlock()
	f.latest().inject(json.RawMessage(`{"jsonrpc":"2.0","method":"notifications/tools/list_changed"}`))

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := reg.Get("mcp__srv__fresh"); ok {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("list_changed never registered the new tool")
}

func TestManagerBadSchemaFallsBackPermissive(t *testing.T) {
	handler := func(method string, req Request) (any, *RPCError) {
		if method == "tools/list" {
			return listToolsResult{Tools: []ToolInfo{{Name: "odd", Description: "bad schema",
				InputSchema: json.RawMessage(`{"type":12345}`)}}}, nil // valid JSON, uncompilable schema
		}
		return stdHandler(ProtocolVersion)(method, req)
	}
	f := &transportFactory{t: t, handler: handler}
	m, reg := startManager(t, map[string]ServerConfig{"srv": {Command: "x"}}, f, nil)
	waitState(t, m, "srv", StateReady)

	tool, ok := reg.Get("mcp__srv__odd")
	if !ok {
		t.Fatal("tool with bad schema must still register")
	}
	if string(tool.Schema()) != string(permissiveSchema) {
		t.Fatalf("schema = %s, want permissive fallback", tool.Schema())
	}
}

func TestParseServersEnvExpansionAndValidation(t *testing.T) {
	t.Setenv("MIXI_TEST_TOKEN", "sekret")
	cfgs, err := ParseServers(map[string]json.RawMessage{
		"gh": json.RawMessage(`{"command":"gh-mcp","args":["stdio"],"env":{"TOKEN":"${MIXI_TEST_TOKEN}"},"timeoutMs":1500}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfgs["gh"].Env["TOKEN"] != "sekret" {
		t.Fatalf("env expansion = %q", cfgs["gh"].Env["TOKEN"])
	}
	if cfgs["gh"].Timeout() != 1500*time.Millisecond {
		t.Fatalf("timeout = %v", cfgs["gh"].Timeout())
	}

	if _, err := ParseServers(map[string]json.RawMessage{"bad": json.RawMessage(`{}`)}); err == nil {
		t.Fatal("missing command must error")
	}
	if _, err := ParseServers(map[string]json.RawMessage{"bad": json.RawMessage(`"nope"`)}); err == nil {
		t.Fatal("non-object config must error")
	}
}

func TestRegisteredToolNameSanitization(t *testing.T) {
	got := RegisteredToolName("my server.io", "do/thing")
	if got != "mcp__my_server_io__do_thing" {
		t.Fatalf("name = %q", got)
	}
}
