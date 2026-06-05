package tui

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"
)

// Run starts the interactive session and blocks until the user quits. The
// agent event subscription is pumped by the bridge goroutine; everything
// else happens inside the tea update loop. Output goes only through the tea
// renderer — logs must already be routed to a file by the caller.
func Run(ctx context.Context, d Deps, opts ...tea.ProgramOption) error {
	m := newRootModel(d)
	opts = append([]tea.ProgramOption{
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
		tea.WithContext(ctx),
	}, opts...)
	p := tea.NewProgram(m, opts...)

	events, unsubscribe := d.Agent.Subscribe()
	defer unsubscribe()
	bridgeCtx, stopBridge := context.WithCancel(ctx)
	defer stopBridge()
	go runBridge(bridgeCtx, p, events)

	_, err := p.Run()
	return err
}
