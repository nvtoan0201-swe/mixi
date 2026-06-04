package modes

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/user/mixi-agent/internal/agent"
	"github.com/user/mixi-agent/internal/agent/agenttest"
	"github.com/user/mixi-agent/internal/ai"
	"github.com/user/mixi-agent/internal/compact"
	"github.com/user/mixi-agent/internal/session"
	"github.com/user/mixi-agent/internal/tools"
)

// echoTool is a minimal parallel tool that records being called.
type echoTool struct{ called bool }

func (e *echoTool) Name() string            { return "echo" }
func (e *echoTool) Description() string     { return "echoes" }
func (e *echoTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (e *echoTool) Mode() tools.ExecMode    { return tools.ExecParallel }
func (e *echoTool) Execute(ctx context.Context, args json.RawMessage, _ chan<- tools.ToolUpdate) (tools.ToolResult, error) {
	e.called = true
	return tools.Text("echoed"), nil
}

// newPrintAgent assembles an agent over a scripted provider stream plus a
// fresh in-memory session store, wired the way main does: the context
// controller's hooks own persistence.
func newPrintAgent(t *testing.T, turns ...agenttest.Turn) (*agent.Agent, session.Storage, *echoTool) {
	t.Helper()
	reg := tools.NewRegistry()
	echo := &echoTool{}
	if err := reg.Register(echo); err != nil {
		t.Fatal(err)
	}
	fake := agenttest.New(turns...)
	m := session.Manager{Root: t.TempDir()}
	store := m.InMemory("/tmp/proj")
	model := ai.Model{API: "test", Provider: "test", ID: "scripted", ContextWindow: 200_000, MaxOutput: 8192}
	ctrl := compact.NewController(compact.ControllerConfig{
		Store: store,
		Model: model,
	})
	a := agent.New(agent.Config{
		Model:   model,
		Tools:   reg,
		Stream:  fake.Stream,
		Hooks:   ctrl.Hooks(),
		History: ctrl.LoadHistory(),
	})
	ctrl.SetNotify(a.Notify)
	return a, store, echo
}

func TestRunPrintTextOutputAndPersistence(t *testing.T) {
	a, store, echo := newPrintAgent(t,
		agenttest.Turn{Text: "calling tool", ToolCalls: []agenttest.ToolCallSpec{{ID: "t1", Name: "echo"}}},
		agenttest.Turn{Text: "final answer"},
	)
	var out, errOut strings.Builder
	code := RunPrint(context.Background(), PrintDeps{Agent: a, Out: &out, ErrOut: &errOut},
		PrintOptions{Prompt: "do it"})
	if code != ExitOK {
		t.Fatalf("exit = %d, stderr: %s", code, errOut.String())
	}
	if got := out.String(); got != "final answer\n" {
		t.Errorf("stdout = %q, want final assistant text only", got)
	}
	if !echo.called {
		t.Error("scripted tool call never executed")
	}
	// Session: user prompt, assistant turn 1, tool result, assistant turn 2.
	entries := store.Entries()
	if len(entries) != 4 {
		t.Fatalf("persisted %d entries, want 4", len(entries))
	}
	roles := []ai.Role{}
	for _, e := range entries {
		me, ok := e.(*session.MessageEntry)
		if !ok {
			t.Fatalf("entry type %T", e)
		}
		roles = append(roles, me.Message.MsgRole())
	}
	want := []ai.Role{ai.RoleUser, ai.RoleAssistant, ai.RoleToolResult, ai.RoleAssistant}
	for i := range want {
		if roles[i] != want[i] {
			t.Fatalf("roles = %v, want %v", roles, want)
		}
	}
}

func TestRunPrintJSONStream(t *testing.T) {
	a, _, _ := newPrintAgent(t,
		agenttest.Turn{Text: "step", ToolCalls: []agenttest.ToolCallSpec{{ID: "t1", Name: "echo"}}},
		agenttest.Turn{Text: "done"},
	)
	var out strings.Builder
	code := RunPrint(context.Background(), PrintDeps{Agent: a, Out: &out, ErrOut: &strings.Builder{}},
		PrintOptions{Prompt: "go", JSON: true})
	if code != ExitOK {
		t.Fatalf("exit = %d", code)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	var events []string
	for _, ln := range lines {
		var m map[string]any
		if err := json.Unmarshal([]byte(ln), &m); err != nil {
			t.Fatalf("invalid JSONL line %q: %v", ln, err)
		}
		ev, _ := m["event"].(string)
		if ev == "" {
			t.Fatalf("line lacks event field: %q", ln)
		}
		events = append(events, ev)
	}
	if events[0] != "agent_start" || events[len(events)-1] != "agent_end" {
		t.Errorf("stream must be bracketed by agent_start/agent_end: %v", events)
	}
	joined := strings.Join(events, ",")
	for _, must := range []string{"message_end", "tool_start", "tool_end", "text_delta", "turn_start", "turn_end"} {
		if !strings.Contains(joined, must) {
			t.Errorf("missing %s in event stream: %v", must, events)
		}
	}
}

func TestRunPrintFollowUpsExtendRun(t *testing.T) {
	a, store, _ := newPrintAgent(t,
		agenttest.Turn{Text: "first"},
		agenttest.Turn{Text: "second"},
	)
	var out strings.Builder
	code := RunPrint(context.Background(), PrintDeps{Agent: a, Out: &out, ErrOut: &strings.Builder{}},
		PrintOptions{Prompt: "one", Messages: []string{"two"}})
	if code != ExitOK {
		t.Fatalf("exit = %d", code)
	}
	if out.String() != "second\n" {
		t.Errorf("stdout = %q, want text of the follow-up turn", out.String())
	}
	if n := len(store.Entries()); n != 4 { // user, asst, user follow-up, asst
		t.Errorf("persisted %d entries, want 4", n)
	}
}

func TestRunPrintErrorStopReasonExitsOne(t *testing.T) {
	a, _, _ := newPrintAgent(t, agenttest.Turn{Err: "model exploded"})
	var out, errOut strings.Builder
	code := RunPrint(context.Background(), PrintDeps{Agent: a, Out: &out, ErrOut: &errOut},
		PrintOptions{Prompt: "boom"})
	if code != ExitRunErr {
		t.Fatalf("exit = %d, want %d", code, ExitRunErr)
	}
	if !strings.Contains(errOut.String(), "model exploded") {
		t.Errorf("stderr should carry the error: %q", errOut.String())
	}
}

func TestRunPrintStatsOnStderr(t *testing.T) {
	a, _, _ := newPrintAgent(t, agenttest.Turn{Text: "hi"})
	var out, errOut strings.Builder
	code := RunPrint(context.Background(), PrintDeps{Agent: a, Out: &out, ErrOut: &errOut},
		PrintOptions{Prompt: "hello", PrintStats: true})
	if code != ExitOK {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(errOut.String(), "turns=1") || !strings.Contains(errOut.String(), "tokens:") {
		t.Errorf("stats missing from stderr: %q", errOut.String())
	}
	if strings.Contains(out.String(), "turns=") {
		t.Error("stats must not pollute stdout")
	}
}

func TestRunPrintAbortExits130(t *testing.T) {
	a, _, _ := newPrintAgent(t, agenttest.Turn{WaitCtx: true})
	ctx, cancel := context.WithCancel(context.Background())
	go func() { a.Abort() }() // races the run start; cancel ctx as backstop
	go cancel()
	var out strings.Builder
	code := RunPrint(ctx, PrintDeps{Agent: a, Out: &out, ErrOut: &strings.Builder{}},
		PrintOptions{Prompt: "long task"})
	if code != ExitSIGINT {
		t.Fatalf("exit = %d, want %d", code, ExitSIGINT)
	}
}
