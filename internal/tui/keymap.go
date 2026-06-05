package tui

import "github.com/charmbracelet/bubbles/key"

// keyMap is the single source of truth for every binding: handlers switch on
// these and help text comes from the same definitions.
type keyMap struct {
	Send      key.Binding // send prompt / steer mid-run
	FollowUp  key.Binding // queue as follow-up
	Interrupt key.Binding // interrupt run / close modal / clear input
	ClearQuit key.Binding // clear input; twice within 1s quits
	Quit      key.Binding // quit immediately (empty input)
	Thinking  key.Binding // cycle thinking level
	Model     key.Binding // cycle model
	ToolsFold key.Binding // toggle tool output expansion
	ThinkFold key.Binding // toggle thinking blocks
	Editor    key.Binding // open $EDITOR for input
	Redraw    key.Binding
	ScrollUp  key.Binding
	ScrollDn  key.Binding
	HistPrev  key.Binding // input history (empty input)
	HistNext  key.Binding
	Complete  key.Binding // accept slash-command suggestion

	// Modal-only bindings.
	Allow  key.Binding
	Deny   key.Binding
	Always key.Binding
}

func defaultKeyMap() keyMap {
	return keyMap{
		Send:      key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "send / steer")),
		FollowUp:  key.NewBinding(key.WithKeys("alt+enter"), key.WithHelp("alt+enter", "queue follow-up")),
		Interrupt: key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "interrupt / close")),
		ClearQuit: key.NewBinding(key.WithKeys("ctrl+c"), key.WithHelp("ctrl+c", "clear; twice quit")),
		Quit:      key.NewBinding(key.WithKeys("ctrl+d"), key.WithHelp("ctrl+d", "quit")),
		Thinking:  key.NewBinding(key.WithKeys("shift+tab"), key.WithHelp("shift+tab", "thinking level")),
		Model:     key.NewBinding(key.WithKeys("ctrl+p"), key.WithHelp("ctrl+p", "cycle model")),
		ToolsFold: key.NewBinding(key.WithKeys("ctrl+o"), key.WithHelp("ctrl+o", "expand tools")),
		ThinkFold: key.NewBinding(key.WithKeys("ctrl+t"), key.WithHelp("ctrl+t", "expand thinking")),
		Editor:    key.NewBinding(key.WithKeys("ctrl+g"), key.WithHelp("ctrl+g", "$EDITOR")),
		Redraw:    key.NewBinding(key.WithKeys("ctrl+l"), key.WithHelp("ctrl+l", "redraw")),
		ScrollUp:  key.NewBinding(key.WithKeys("pgup"), key.WithHelp("pgup", "scroll up")),
		ScrollDn:  key.NewBinding(key.WithKeys("pgdown"), key.WithHelp("pgdn", "scroll down")),
		HistPrev:  key.NewBinding(key.WithKeys("up"), key.WithHelp("up", "history prev")),
		HistNext:  key.NewBinding(key.WithKeys("down"), key.WithHelp("down", "history next")),
		Complete:  key.NewBinding(key.WithKeys("tab"), key.WithHelp("tab", "complete command")),

		Allow:  key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "allow")),
		Deny:   key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "deny")),
		Always: key.NewBinding(key.WithKeys("A"), key.WithHelp("A", "always this session")),
	}
}
