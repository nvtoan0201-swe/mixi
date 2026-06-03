// Package anthropic implements the Anthropic Messages API SSE streaming provider.
// Self-registers via init() — callers import _ "github.com/user/mixi-agent/internal/ai/providers/anthropic".
package anthropic

import (
	"context"

	"github.com/user/mixi-agent/internal/ai"
)

const channelBuffer = 100

func init() {
	ai.Register(&Provider{})
}

// Provider implements ai.Provider for the Anthropic Messages API.
type Provider struct{}

func (p *Provider) API() string { return "anthropic-messages" }

// Stream opens a streaming connection to the Anthropic API and returns a channel of events.
// The channel is closed when the stream ends or an error occurs.
func (p *Provider) Stream(ctx context.Context, model ai.Model, c ai.Context, opts ai.StreamOptions) <-chan ai.AssistantMessageEvent {
	ch := make(chan ai.AssistantMessageEvent, channelBuffer)
	go func() {
		defer close(ch)
		if err := p.stream(ctx, model, c, opts, ch); err != nil {
			select {
			case ch <- ai.ErrorEvent{Reason: ai.StopReasonError, Err: err}:
			case <-ctx.Done():
			}
		}
	}()
	return ch
}
