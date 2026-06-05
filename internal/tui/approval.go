package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/user/mixi-agent/internal/perm"
)

var (
	modalBorder = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("11")).
			Padding(0, 1)
	modalTitle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("11"))
	modalHint  = lipgloss.NewStyle().Faint(true)
)

// approvalModel is the permission modal. It owns the keyboard while open;
// there is deliberately no timeout — the user decides. The decision travels
// back over the request's buffered Reply channel, unblocking the permission
// engine in the agent goroutine.
type approvalModel struct {
	ask     perm.PendingAsk
	preview viewport.Model
	keys    keyMap
	width   int
	replied bool
}

func newApprovalModal(ask perm.PendingAsk, keys keyMap, width, height int) *approvalModel {
	m := &approvalModel{ask: ask, keys: keys}
	m.preview = viewport.New(0, 0)
	m.setSize(width, height)
	m.preview.SetContent(colorizeDiff(ask.Req.Preview))
	return m
}

// setSize fits the preview to its content so the modal never grows past the
// terminal — a too-tall modal would push its own title off-screen.
func (m *approvalModel) setSize(width, height int) {
	m.width = width
	lines := strings.Count(m.ask.Req.Preview, "\n") + 1
	maxH := max(min(height-10, 16), 3)
	m.preview.Width = max(min(width-6, 100), 10)
	m.preview.Height = min(lines, maxH)
}

// reply sends the decision exactly once; the channel is buffered so the UI
// never blocks on the agent goroutine.
func (m *approvalModel) reply(d perm.AskDecision) {
	if m.replied {
		return
	}
	m.replied = true
	m.ask.Reply <- d
}

// update handles modal keys; done=true means the modal should close.
func (m *approvalModel) update(msg tea.KeyMsg) (done bool) {
	switch {
	case key.Matches(msg, m.keys.Allow):
		m.reply(perm.AskAllow)
		return true
	case key.Matches(msg, m.keys.Always):
		m.reply(perm.AskAlways)
		return true
	case key.Matches(msg, m.keys.Deny), key.Matches(msg, m.keys.Interrupt):
		m.reply(perm.AskDeny)
		return true
	case key.Matches(msg, m.keys.ScrollUp):
		m.preview.HalfPageUp()
	case key.Matches(msg, m.keys.ScrollDn):
		m.preview.HalfPageDown()
	}
	return false
}

func (m *approvalModel) view() string {
	title := modalTitle.Render("Permission required: " + m.ask.Req.Tool)
	why := m.ask.Req.Why
	hint := modalHint.Render("[a]llow  [d]eny  [A]lways this session  esc=deny  pgup/pgdn=scroll")
	body := title
	if why != "" {
		body += "\n" + why
	}
	if m.ask.Req.Preview != "" {
		body += "\n\n" + m.preview.View()
	}
	body += "\n\n" + hint
	return modalBorder.Width(min(m.width-4, 104)).Render(body)
}
