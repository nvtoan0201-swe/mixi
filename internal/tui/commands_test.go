package tui

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/user/mixi-agent/internal/agent"
	"github.com/user/mixi-agent/internal/ai"
	"github.com/user/mixi-agent/internal/mcp"
	"github.com/user/mixi-agent/internal/perm"
	"github.com/user/mixi-agent/internal/session"
)

// commandFixture builds a root model with real backends (in-memory session,
// permission engine, idle agent) but no running tea program — slash command
// handlers are plain state transitions and can be tested directly.
func commandFixture(t *testing.T) (*rootModel, session.Storage) {
	t.Helper()
	dir := t.TempDir()
	store := (&session.Manager{Root: filepath.Join(dir, "sessions")}).InMemory(dir)
	policy, err := perm.NewPolicy(perm.PolicyConfig{Mode: "prompt"})
	if err != nil {
		t.Fatal(err)
	}
	eng := perm.NewEngine(policy, perm.HeadlessAsker{}, dir)
	ag := agent.New(agent.Config{Model: testModel})
	other := ai.Model{Provider: "test", ID: "other", DisplayName: "Other", ContextWindow: 1000}
	m := newRootModel(Deps{
		Agent: ag, Engine: eng, Store: store,
		Models: []ai.Model{testModel, other},
	})
	return m, store
}

func lastNotice(t *testing.T, m *rootModel) string {
	t.Helper()
	for i := len(m.transcript.blocks) - 1; i >= 0; i-- {
		if n, ok := m.transcript.blocks[i].(*noticeBlock); ok {
			return n.text
		}
	}
	t.Fatal("no notice block in transcript")
	return ""
}

func TestCmdModelCycleAndExplicit(t *testing.T) {
	m, store := commandFixture(t)

	cmdModel(m, "") // cycle: scripted → other
	if got := m.deps.Agent.Model().ID; got != "other" {
		t.Fatalf("cycled model = %q, want other", got)
	}
	cmdModel(m, "test/scripted") // explicit provider/id
	if got := m.deps.Agent.Model().ID; got != "scripted" {
		t.Fatalf("explicit model = %q, want scripted", got)
	}
	cmdModel(m, "nope")
	if !strings.Contains(lastNotice(t, m), "Unknown model") {
		t.Fatal("unknown model must notice, not change state")
	}

	changes := 0
	for _, e := range store.Entries() {
		if _, ok := e.(*session.ModelChangeEntry); ok {
			changes++
		}
	}
	if changes != 2 {
		t.Fatalf("model change entries = %d, want 2", changes)
	}
}

func TestCmdModeCycleAndExplicit(t *testing.T) {
	m, _ := commandFixture(t)

	cmdMode(m, "") // prompt → auto-edit
	if got := m.deps.Engine.Mode(); got != perm.ModeAutoEdit {
		t.Fatalf("cycled mode = %q, want auto-edit", got)
	}
	cmdMode(m, "yolo")
	if got := m.deps.Engine.Mode(); got != perm.ModeYolo {
		t.Fatalf("explicit mode = %q, want yolo", got)
	}
	cmdMode(m, "bogus")
	if m.deps.Engine.Mode() != perm.ModeYolo {
		t.Fatal("bogus mode must not change state")
	}
}

func TestCmdNamePinTree(t *testing.T) {
	m, store := commandFixture(t)

	cmdName(m, "auth refactor")
	cmdPin(m, "") // usage error: no label
	if !strings.Contains(lastNotice(t, m), "Usage: /pin") {
		t.Fatal("empty pin label must show usage")
	}
	// Labels target the chain leaf; off-chain entries (name) don't move it,
	// so pin something that does: a message entry.
	if err := store.Append(&session.MessageEntry{Message: ai.UserMessage{
		Content: []ai.Content{ai.TextContent{Text: "hi"}},
	}}); err != nil {
		t.Fatal(err)
	}
	cmdPin(m, "milestone")
	var labels, infos int
	for _, e := range store.Entries() {
		switch e.(type) {
		case *session.LabelEntry:
			labels++
		case *session.SessionInfoEntry:
			infos++
		}
	}
	if labels != 1 || infos != 1 {
		t.Fatalf("labels=%d infos=%d, want 1/1", labels, infos)
	}

	cmdTree(m, "")
	tree := lastNotice(t, m)
	if !strings.Contains(tree, "session_info") || !strings.Contains(tree, "label") {
		t.Fatalf("/tree must list entry types:\n%s", tree)
	}
}

func TestDispatchUnknownAndPermissions(t *testing.T) {
	m, _ := commandFixture(t)

	if cmd := m.dispatchCommand("/bogus arg"); cmd != nil {
		t.Fatal("unknown command must not return a tea.Cmd")
	}
	if !strings.Contains(lastNotice(t, m), "Unknown command /bogus") {
		t.Fatal("unknown command must notice")
	}

	cmdPermissions(m, "")
	if got := lastNotice(t, m); !strings.Contains(got, "Permission mode: prompt") ||
		!strings.Contains(got, "Session grants: none") {
		t.Fatalf("/permissions output:\n%s", got)
	}
}

// fakeFleet stands in for the MCP manager in /mcp tests.
type fakeFleet struct {
	statuses     []mcp.ServerStatus
	reconnected  []string
	reconnectErr error
}

func (f *fakeFleet) Status() []mcp.ServerStatus { return f.statuses }
func (f *fakeFleet) Reconnect(name string) error {
	f.reconnected = append(f.reconnected, name)
	return f.reconnectErr
}

func TestCmdMCPStatusAndReconnect(t *testing.T) {
	m, _ := commandFixture(t)

	// No fleet wired: helpful notice, no panic.
	cmdMCP(m, "")
	if !strings.Contains(lastNotice(t, m), "No MCP servers configured") {
		t.Fatal("nil fleet must explain how to configure MCP")
	}

	fleet := &fakeFleet{statuses: []mcp.ServerStatus{
		{Name: "github", State: mcp.StateReady, Tools: 12},
		{Name: "jira", State: mcp.StateFailed, Tools: 0},
	}}
	m.deps.MCP = fleet
	cmdMCP(m, "")
	out := lastNotice(t, m)
	for _, want := range []string{"github", "ready", "12 tools", "jira", "failed", "reconnect"} {
		if !strings.Contains(out, want) {
			t.Fatalf("/mcp output missing %q:\n%s", want, out)
		}
	}

	cmdMCP(m, "reconnect jira")
	if len(fleet.reconnected) != 1 || fleet.reconnected[0] != "jira" {
		t.Fatalf("reconnect dispatch = %v", fleet.reconnected)
	}
	fleet.reconnectErr = errors.New("not failed")
	cmdMCP(m, "reconnect github")
	if !strings.Contains(lastNotice(t, m), "not failed") {
		t.Fatal("reconnect error must surface to the user")
	}
}

func TestCycleThinkingPersistsEntry(t *testing.T) {
	m, store := commandFixture(t)

	m.cycleThinking() // off → low
	if got := m.deps.Agent.Thinking(); got != ai.ThinkingLow {
		t.Fatalf("thinking = %q, want low", got)
	}
	found := false
	for _, e := range store.Entries() {
		if tl, ok := e.(*session.ThinkingLevelChangeEntry); ok && tl.ThinkingLevel == ai.ThinkingLow {
			found = true
		}
	}
	if !found {
		t.Fatal("thinking change must persist a session entry")
	}
}
