package tui

import (
	"context"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/user/mixi-agent/internal/agent"
	"github.com/user/mixi-agent/internal/ai"
	"github.com/user/mixi-agent/internal/perm"
	"github.com/user/mixi-agent/internal/session"
)

const ctrlCWindowMs = 1000

// thinkingCycle is the shift+tab rotation order.
var thinkingCycle = []ai.ThinkingLevel{ai.ThinkingOff, ai.ThinkingLow, ai.ThinkingMedium, ai.ThinkingHigh}

// handleKey routes keystrokes: the modal owns the keyboard while open, then
// global bindings, then the editor.
func (m *rootModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.modal != nil {
		// Double ctrl+c still quits — never trap the user in a modal.
		if key.Matches(msg, m.keys.ClearQuit) && m.armCtrlC() {
			m.modal.reply(perm.AskDeny) // quitting abandons the request
			return m, tea.Quit
		}
		if m.modal.update(msg) {
			m.closeModal()
		}
		return m, nil
	}

	switch {
	case key.Matches(msg, m.keys.ClearQuit):
		if m.armCtrlC() {
			return m, tea.Quit
		}
		m.editor.clear()
		return m, nil
	case key.Matches(msg, m.keys.Quit):
		if m.editor.value() == "" {
			return m, tea.Quit
		}
		return m, nil
	case key.Matches(msg, m.keys.Interrupt):
		return m, m.interrupt()
	case key.Matches(msg, m.keys.Send):
		return m, m.submit(false)
	case key.Matches(msg, m.keys.FollowUp):
		return m, m.submit(true)
	case key.Matches(msg, m.keys.Thinking):
		m.cycleThinking()
		return m, nil
	case key.Matches(msg, m.keys.Model):
		return m, cmdModel(m, "")
	case key.Matches(msg, m.keys.ToolsFold):
		m.transcript.expandTools = !m.transcript.expandTools
		m.transcript.refresh()
		return m, nil
	case key.Matches(msg, m.keys.ThinkFold):
		m.transcript.expandThinking = !m.transcript.expandThinking
		m.transcript.refresh()
		return m, nil
	case key.Matches(msg, m.keys.Editor):
		return m, m.editor.openExternalEditor()
	case key.Matches(msg, m.keys.Redraw):
		return m, tea.ClearScreen
	case key.Matches(msg, m.keys.ScrollUp), key.Matches(msg, m.keys.ScrollDn):
		vp, cmd := m.transcript.vp.Update(msg)
		m.transcript.vp = vp
		return m, cmd
	case key.Matches(msg, m.keys.HistPrev):
		if m.editor.browsing() && m.editor.histPrev() {
			return m, nil
		}
	case key.Matches(msg, m.keys.HistNext):
		if m.editor.browsing() && m.editor.histNext() {
			return m, nil
		}
	case key.Matches(msg, m.keys.Complete):
		if m.editor.complete(m.cmds) {
			return m, nil
		}
	}
	return m, m.editor.update(msg)
}

// armCtrlC implements "ctrl+c clears; twice within 1s quits". It reports
// whether this press is the quitting second one.
func (m *rootModel) armCtrlC() bool {
	now := time.Now().UnixMilli()
	if m.ctrlCArmed != 0 && now-m.ctrlCArmed < ctrlCWindowMs {
		return true
	}
	m.ctrlCArmed = now
	return false
}

// interrupt aborts an active run, otherwise clears the input.
func (m *rootModel) interrupt() tea.Cmd {
	if m.running && m.cancelRun != nil {
		m.cancelRun()
		m.transcript.notice("Interrupting…", false)
		return nil
	}
	m.editor.clear()
	return nil
}

// submit dispatches the editor content: slash command, steer (mid-run),
// follow-up queue, or a fresh run.
func (m *rootModel) submit(asFollowUp bool) tea.Cmd {
	text := m.editor.value()
	if text == "" {
		return nil
	}
	m.editor.remember(text)
	m.editor.clear()

	if strings.HasPrefix(text, "/") {
		return m.dispatchCommand(text)
	}
	switch {
	case m.running && asFollowUp:
		if err := m.deps.Agent.FollowUp(userMsg(text)); err != nil {
			m.transcript.notice("Follow-up queue: "+err.Error(), true)
		} else {
			m.transcript.notice("Queued as follow-up.", false)
		}
		return nil
	case m.running:
		if err := m.deps.Agent.Steer(userMsg(text)); err != nil {
			m.transcript.notice("Steering queue: "+err.Error(), true)
		}
		return nil
	default:
		return m.startRun(text)
	}
}

// startRun launches Agent.Prompt off the UI goroutine; it blocks until the
// run ends and resolves to runDoneMsg. The run context is created here so
// Esc can cancel it even before the prompt goroutine gets scheduled.
func (m *rootModel) startRun(text string) tea.Cmd {
	ctx, cancel := context.WithCancel(context.Background())
	m.running = true
	m.status.running = true
	m.cancelRun = cancel
	ag := m.deps.Agent
	return func() tea.Msg {
		return runDoneMsg{err: ag.Prompt(ctx, text)}
	}
}

// dispatchCommand resolves "/name arg…" against the command table.
func (m *rootModel) dispatchCommand(text string) tea.Cmd {
	name, arg, _ := strings.Cut(text, " ")
	arg = strings.TrimSpace(arg)
	for _, c := range m.cmds {
		if c.name == name {
			return c.run(m, arg)
		}
	}
	m.transcript.notice("Unknown command "+name+" (try tab completion)", true)
	return nil
}

func (m *rootModel) cycleThinking() {
	cur := m.deps.Agent.Thinking()
	if cur == "" {
		cur = ai.ThinkingOff // unset config means off; cycle to low, not off
	}
	next := thinkingCycle[0]
	for i, lv := range thinkingCycle {
		if lv == cur {
			next = thinkingCycle[(i+1)%len(thinkingCycle)]
			break
		}
	}
	m.deps.Agent.SetThinking(next)
	m.status.thinking = next
	m.appendEntry(&session.ThinkingLevelChangeEntry{ThinkingLevel: next})
	m.transcript.notice("Thinking: "+string(next), false)
}

func userMsg(text string) agent.AgentMessage {
	return agent.ModelMessage{Msg: ai.UserMessage{
		Content:   []ai.Content{ai.TextContent{Text: text}},
		Timestamp: time.Now().UnixMilli(),
	}}
}
