package tui

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/exp/teatest"

	"github.com/user/mixi-agent/internal/agent"
	"github.com/user/mixi-agent/internal/agent/agenttest"
	"github.com/user/mixi-agent/internal/ai"
	"github.com/user/mixi-agent/internal/compact"
	"github.com/user/mixi-agent/internal/perm"
	"github.com/user/mixi-agent/internal/session"
	"github.com/user/mixi-agent/internal/tools"
)

const waitDur = 10 * time.Second

var testModel = ai.Model{
	API: "test", Provider: "test", ID: "scripted",
	DisplayName: "Test Scripted", ContextWindow: 200_000, MaxOutput: 8_192,
}

// permFilter mirrors the cmd/mixi adapter so the TUI E2E exercises the same
// decision path the binary uses.
type permFilter struct{ eng *perm.Engine }

func (p permFilter) FilterToolCall(ctx context.Context, call ai.ToolCall) (json.RawMessage, *agent.BlockDecision, error) {
	res := p.eng.Decide(ctx, call)
	if res.Allow {
		return call.Args, nil, nil
	}
	return call.Args, &agent.BlockDecision{Reason: res.Reason}, nil
}

// harness is one fully wired interactive session against a scripted
// provider: real tools in a temp cwd, real permission engine, real session
// store, events pumped through the bridge into a teatest program.
type harness struct {
	t     *testing.T
	tm    *teatest.TestModel
	agent *agent.Agent
	eng   *perm.Engine
	store session.Storage
	dir   string
}

func newHarness(t *testing.T, mode perm.Mode, turns ...agenttest.Turn) *harness {
	t.Helper()
	dir := t.TempDir()
	mgr := &session.Manager{Root: filepath.Join(dir, "sessions")}
	store, err := mgr.Create(dir)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	reg := tools.NewRegistry()
	jobs, err := tools.RegisterBuiltins(reg, tools.Options{Cwd: dir})
	if err != nil {
		t.Fatalf("register tools: %v", err)
	}
	t.Cleanup(jobs.KillAll)

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctrl := compact.NewController(compact.ControllerConfig{
		Store: store, Model: testModel, Enabled: false, Log: log,
	})

	policy, err := perm.NewPolicy(perm.PolicyConfig{Mode: string(mode)})
	if err != nil {
		t.Fatalf("policy: %v", err)
	}
	var ag *agent.Agent
	asker := perm.NotifyAsker{Notify: func(p perm.PendingAsk) {
		ag.Notify(agent.EvPermissionAsk{Req: p})
	}}
	eng := perm.NewEngine(policy, asker, dir)

	provider := agenttest.New(turns...)
	ag = agent.New(agent.Config{
		Model:   testModel,
		Tools:   reg,
		Filters: []agent.ToolCallFilter{permFilter{eng}},
		Stream:  provider.Stream,
		Hooks:   ctrl.Hooks(),
		Log:     log,
	})
	ctrl.SetNotify(ag.Notify)

	m := newRootModel(Deps{
		Agent: ag, Engine: eng, Compactor: ctrl, Store: store,
		Jobs: jobs, Models: []ai.Model{testModel},
	})
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(100, 30))

	events, unsubscribe := ag.Subscribe()
	ctx, cancel := context.WithCancel(context.Background())
	go runBridge(ctx, tm, events)
	t.Cleanup(func() { cancel(); unsubscribe() })

	return &harness{t: t, tm: tm, agent: ag, eng: eng, store: store, dir: dir}
}

// waitFor blocks until every substring has appeared. WaitFor consumes the
// output stream, so strings that may render in the same burst of frames
// must be awaited together in one call, not sequentially.
func (h *harness) waitFor(subs ...string) {
	h.t.Helper()
	teatest.WaitFor(h.t, h.tm.Output(), func(bts []byte) bool {
		for _, sub := range subs {
			if !bytes.Contains(bts, []byte(sub)) {
				return false
			}
		}
		return true
	}, teatest.WithDuration(waitDur))
}

func (h *harness) prompt(text string) {
	h.tm.Type(text)
	h.tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
}

func (h *harness) quit() {
	h.tm.Send(tea.KeyMsg{Type: tea.KeyCtrlC})
	h.tm.Send(tea.KeyMsg{Type: tea.KeyCtrlC})
	h.tm.WaitFinished(h.t, teatest.WithFinalTimeout(waitDur))
}

// TestInteractiveSessionApprovalFlow drives the full loop: prompt →
// streaming → tool card → approval modal → tool runs → final message →
// quit, and proves nothing was written to os.Stdout outside the renderer.
func TestInteractiveSessionApprovalFlow(t *testing.T) {
	origStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	defer func() { os.Stdout = origStdout }()

	h := newHarness(t, perm.ModePrompt,
		agenttest.Turn{Text: "Writing the file.", ToolCalls: []agenttest.ToolCallSpec{
			{ID: "t1", Name: "write", Args: `{"path":"hello.txt","content":"hi"}`},
		}},
		agenttest.Turn{Text: "All done now."},
	)

	h.prompt("create hello.txt")
	h.waitFor("Permission required: write")
	h.tm.Type("a") // allow once
	h.waitFor("All done now.")
	h.quit()

	got, err := os.ReadFile(filepath.Join(h.dir, "hello.txt"))
	if err != nil || string(got) != "hi" {
		t.Fatalf("tool did not write the file: %q, %v", got, err)
	}

	// Session file: user prompt + 2 assistant messages + tool result.
	msgs := 0
	for _, e := range h.store.Entries() {
		if _, ok := e.(*session.MessageEntry); ok {
			msgs++
		}
	}
	if msgs != 4 {
		t.Fatalf("session message entries = %d, want 4", msgs)
	}

	// Stdout guard: the renderer (teatest buffer) is the only writer.
	w.Close()
	leaked, _ := io.ReadAll(r)
	if len(leaked) != 0 {
		t.Fatalf("stdout received %d bytes outside the renderer: %q", len(leaked), leaked)
	}
}

// TestAlwaysGrantAutoAllowsSecondCall: 'A' records a session grant, so the
// next call matching its generalization runs without a second modal.
func TestAlwaysGrantAutoAllowsSecondCall(t *testing.T) {
	h := newHarness(t, perm.ModePrompt,
		agenttest.Turn{Text: "First write.", ToolCalls: []agenttest.ToolCallSpec{
			{ID: "t1", Name: "write", Args: `{"path":"one.txt","content":"1"}`},
		}},
		agenttest.Turn{Text: "Second write.", ToolCalls: []agenttest.ToolCallSpec{
			{ID: "t2", Name: "write", Args: `{"path":"two.txt","content":"2"}`},
		}},
		agenttest.Turn{Text: "Both files written."},
	)

	h.prompt("write both files")
	h.waitFor("Permission required: write")
	h.tm.Type("A") // always this session
	// Reaching the final text proves call 2 never blocked on a modal.
	h.waitFor("Both files written.")

	if grants := h.eng.Grants(); len(grants) != 1 {
		t.Fatalf("session grants = %v, want exactly one", grants)
	}
	for _, name := range []string{"one.txt", "two.txt"} {
		if _, err := os.Stat(filepath.Join(h.dir, name)); err != nil {
			t.Fatalf("%s missing: %v", name, err)
		}
	}
	h.quit()
}

// TestEscInterruptsMidStream: Esc aborts a blocked stream; the run ends as
// aborted and the UI returns to idle.
func TestEscInterruptsMidStream(t *testing.T) {
	h := newHarness(t, perm.ModeYolo, agenttest.Turn{WaitCtx: true})

	h.prompt("hang forever")
	h.waitFor("running")
	h.tm.Send(tea.KeyMsg{Type: tea.KeyEsc})
	// The abort is near-instant: both frames land in the same burst.
	h.waitFor("Interrupting…", "context canceled")
	h.quit()
}

// TestSingleCtrlCClearsNotQuits: one ctrl+c clears the input; only the
// second within the window quits.
func TestSingleCtrlCClearsNotQuits(t *testing.T) {
	h := newHarness(t, perm.ModeYolo, agenttest.Turn{Text: "hi"})

	h.tm.Type("draft text")
	h.tm.Send(tea.KeyMsg{Type: tea.KeyCtrlC})
	// Still alive: a slash command round-trips through the update loop.
	time.Sleep(1100 * time.Millisecond) // let the ctrl+c quit window lapse
	h.tm.Type("/cost")
	h.tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	h.waitFor("Session cost:")
	h.quit()
}

// TestModalDenySendsDenial: 'd' denies; the model sees the denial as an
// error tool result and the run continues to the next turn.
func TestModalDenySendsDenial(t *testing.T) {
	h := newHarness(t, perm.ModePrompt,
		agenttest.Turn{Text: "Trying a write.", ToolCalls: []agenttest.ToolCallSpec{
			{ID: "t1", Name: "write", Args: `{"path":"no.txt","content":"x"}`},
		}},
		agenttest.Turn{Text: "Understood, denied."},
	)

	h.prompt("try it")
	h.waitFor("Permission required: write")
	h.tm.Type("d")
	h.waitFor("Understood, denied.")
	if _, err := os.Stat(filepath.Join(h.dir, "no.txt")); !os.IsNotExist(err) {
		t.Fatal("denied write must not create the file")
	}
	h.quit()
}
