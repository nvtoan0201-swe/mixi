// Package tui implements the interactive Bubble Tea frontend: streaming
// transcript, tool cards, permission approval modal, status bar, slash
// commands, and input editor. Agent goroutines never touch the tea program
// directly — every event crosses through the bridge below.
package tui

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/user/mixi-agent/internal/agent"
)

// agentMsg envelopes one agent event for the tea update loop.
type agentMsg struct{ ev agent.Event }

// sender is the slice of *tea.Program the bridge needs (tests inject a
// recorder).
type sender interface{ Send(tea.Msg) }

// runBridge pumps agent events into the program. It is the ONLY goroutine
// calling Send for agent events; tea serializes delivery internally. It
// exits when the subscription closes or ctx is cancelled.
func runBridge(ctx context.Context, p sender, events <-chan agent.Event) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-events:
			if !ok {
				return
			}
			p.Send(agentMsg{ev})
		}
	}
}
