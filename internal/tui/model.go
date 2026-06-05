package tui

import (
	"context"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/user/mixi-agent/internal/agent"
	"github.com/user/mixi-agent/internal/ai"
	"github.com/user/mixi-agent/internal/perm"
	"github.com/user/mixi-agent/internal/session"
)

// Compactor triggers a manual context compaction (/compact).
type Compactor interface {
	Compact(ctx context.Context, focus string) error
}

// JobCounter reports live background jobs for the status bar.
type JobCounter interface{ Live() int }

// Deps wires the TUI to the runtime assembled in cmd/mixi.
type Deps struct {
	Agent     *agent.Agent
	Engine    *perm.Engine
	Compactor Compactor
	Store     session.Storage
	Jobs      JobCounter
	Models    []ai.Model // catalog for /model and ctrl+p
}

// runDoneMsg arrives when Agent.Prompt returns (the run finished).
type runDoneMsg struct{ err error }

var helpStyle = lipgloss.NewStyle().Faint(true)

// rootModel composes the TUI: transcript, editor, status bar, and the
// permission modal when one is pending.
type rootModel struct {
	deps Deps
	keys keyMap
	cmds []commandSpec

	transcript transcriptModel
	editor     editorModel
	status     statusModel
	modal      *approvalModel
	asks       []perm.PendingAsk // queued behind the active modal

	width, height int
	running       bool
	// cancelRun aborts the active run. Owning the run context here (instead
	// of Agent.Abort) closes the race where Esc lands before the prompt
	// goroutine has armed the agent's internal cancel.
	cancelRun  context.CancelFunc
	ctrlCArmed int64 // UnixMilli of the last ctrl+c, 0 = disarmed
}

func newRootModel(d Deps) *rootModel {
	m := &rootModel{
		deps:       d,
		keys:       defaultKeyMap(),
		cmds:       commandTable(),
		transcript: newTranscript(),
		editor:     newEditor(),
	}
	m.status.model = d.Agent.Model()
	m.status.thinking = d.Agent.Thinking()
	if d.Engine != nil {
		m.status.permMode = string(d.Engine.Mode())
	}
	return m
}

func (m *rootModel) Init() tea.Cmd {
	return tea.Batch(m.transcript.spin.Tick, textareaBlink(&m.editor))
}

func textareaBlink(e *editorModel) tea.Cmd { return e.ta.Cursor.BlinkCmd() }

func (m *rootModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.resize(msg.Width, msg.Height)
		return m, nil
	case agentMsg:
		return m, m.applyAgentEvent(msg.ev)
	case runDoneMsg:
		m.running = false
		m.status.running = false
		if m.cancelRun != nil {
			m.cancelRun() // release the run context
			m.cancelRun = nil
		}
		if msg.err != nil {
			m.transcript.notice("Run failed to start: "+msg.err.Error(), true)
		}
		return m, nil
	case compactDoneMsg:
		if msg.err != nil {
			m.transcript.notice("Compaction failed: "+msg.err.Error(), true)
		}
		return m, nil
	case editorDoneMsg:
		if err := m.editor.finishExternalEditor(msg); err != nil {
			m.transcript.notice("Editor: "+err.Error(), true)
		}
		return m, nil
	case spinner.TickMsg:
		return m, m.transcript.tick(msg)
	case tea.MouseMsg:
		vp, cmd := m.transcript.vp.Update(msg)
		m.transcript.vp = vp
		return m, cmd
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	cmd := m.editor.update(msg)
	return m, cmd
}

// applyAgentEvent routes one bridged event. Permission asks open the modal;
// everything else feeds the transcript and status bar.
func (m *rootModel) applyAgentEvent(ev agent.Event) tea.Cmd {
	switch ev := ev.(type) {
	case agent.EvPermissionAsk:
		if ask, ok := ev.Req.(perm.PendingAsk); ok {
			m.enqueueAsk(ask)
		}
	case agent.EvAgentEnd:
		m.status.running = false
	default:
		m.transcript.apply(ev)
	}
	m.status.observe(ev)
	if m.deps.Jobs != nil {
		m.status.jobs = m.deps.Jobs.Live()
	}
	return nil
}

func (m *rootModel) enqueueAsk(ask perm.PendingAsk) {
	if m.modal != nil {
		m.asks = append(m.asks, ask)
		return
	}
	m.modal = newApprovalModal(ask, m.keys, m.width, m.height)
}

// closeModal dismisses the active modal and surfaces the next queued ask.
func (m *rootModel) closeModal() {
	m.modal = nil
	if len(m.asks) > 0 {
		next := m.asks[0]
		m.asks = m.asks[1:]
		m.modal = newApprovalModal(next, m.keys, m.width, m.height)
	}
}

func (m *rootModel) resize(w, h int) {
	m.width, m.height = w, h
	m.status.width = w
	m.editor.setWidth(w)
	// Layout: transcript fills what the help line (1), editor (3) and
	// status bar (1) leave over.
	m.transcript.setSize(w, max(h-5, 3))
	if m.modal != nil {
		m.modal.setSize(w, h)
	}
}

// appendEntry persists a session entry, surfacing failures as notices.
func (m *rootModel) appendEntry(e session.Entry) {
	if err := m.deps.Store.Append(e); err != nil {
		m.transcript.notice("Session write failed: "+err.Error(), true)
	}
}

func (m *rootModel) View() string {
	middle := m.transcript.vp.View()
	if m.modal != nil {
		middle = lipgloss.Place(m.width, m.transcript.vp.Height,
			lipgloss.Center, lipgloss.Center, m.modal.view())
	}
	return middle + "\n" + m.helpLine() + "\n" + m.editor.view() + "\n" + m.status.view()
}

// helpLine shows slash suggestions while typing a command, otherwise a
// compact hint built from the key map.
func (m *rootModel) helpLine() string {
	if sugg := m.editor.suggestions(m.cmds); len(sugg) > 0 {
		parts := make([]string, 0, len(sugg))
		for _, c := range sugg {
			parts = append(parts, c.name)
		}
		return helpStyle.Render(strings.Join(parts, "  ") + "  (tab completes)")
	}
	k := m.keys
	hints := []string{
		k.Send.Help().Key + " " + k.Send.Help().Desc,
		k.Interrupt.Help().Key + " " + k.Interrupt.Help().Desc,
		k.Editor.Help().Key + " " + k.Editor.Help().Desc,
		"/ commands",
	}
	return helpStyle.Render(strings.Join(hints, " • "))
}
