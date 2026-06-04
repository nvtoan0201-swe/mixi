// Package agenttest provides a scripted fake provider for exercising the
// agent loop (and later phases) without any network access. Each Turn in
// the script describes the stream the provider produces for one LLM call,
// in call order.
package agenttest

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/user/mixi-agent/internal/ai"
)

// ToolCallSpec scripts one tool invocation inside a turn.
type ToolCallSpec struct {
	ID   string
	Name string
	Args string // raw JSON object; "" → {}
}

// Turn scripts one provider stream.
type Turn struct {
	// Text streamed as a single text block (omitted when empty).
	Text string
	// ToolCalls streamed as tool-call blocks after the text.
	ToolCalls []ToolCallSpec
	// Err makes the stream terminate with EventError carrying this message.
	// When PartialText is also set, that content streams first — exercising
	// the "partial content is never retried" rule.
	Err         string
	PartialText string
	// Abort makes the stream terminate with StopReasonAborted.
	Abort bool
	// WaitCtx blocks the stream until the caller's ctx is cancelled, then
	// emits an aborted error (exercises abort-during-stream).
	WaitCtx bool
}

// Provider replays scripted turns in call order. Safe for concurrent use;
// calls beyond the script produce an error stream.
type Provider struct {
	mu    sync.Mutex
	turns []Turn
	calls int
	// Contexts records the ai.Context of every call for assertions.
	Contexts []ai.Context
}

func New(turns ...Turn) *Provider { return &Provider{turns: turns} }

// Calls reports how many times Stream was invoked.
func (p *Provider) Calls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

// Stream satisfies the agent's StreamFunc seam.
func (p *Provider) Stream(ctx context.Context, model ai.Model, c ai.Context, opts ai.StreamOptions) <-chan ai.StreamEvent {
	p.mu.Lock()
	idx := p.calls
	p.calls++
	p.Contexts = append(p.Contexts, c)
	var turn Turn
	if idx < len(p.turns) {
		turn = p.turns[idx]
	} else {
		turn = Turn{Err: fmt.Sprintf("agenttest: unscripted call %d", idx)}
	}
	p.mu.Unlock()

	ch := make(chan ai.StreamEvent, 64)
	go func() {
		defer close(ch)
		playTurn(ctx, ch, model, turn)
	}()
	return ch
}

func playTurn(ctx context.Context, ch chan<- ai.StreamEvent, model ai.Model, turn Turn) {
	now := time.Now().UnixMilli()
	partial := ai.AssistantMessage{
		API: model.API, Provider: model.Provider, Model: model.ID,
		StopReason: ai.StopReasonStop, Timestamp: now,
	}
	ch <- ai.EventStart{Partial: partial}

	if turn.WaitCtx {
		<-ctx.Done()
		partial.StopReason = ai.StopReasonAborted
		partial.ErrorMessage = ctx.Err().Error()
		ch <- ai.EventError{Reason: ai.StopReasonAborted, Message: partial}
		return
	}

	idx := 0
	emitText := func(text string) {
		ch <- ai.EventTextStart{ContentIndex: idx, Partial: partial}
		ch <- ai.EventTextDelta{ContentIndex: idx, Delta: text, Partial: partial}
		partial.Content = append(partial.Content, ai.TextContent{Text: text})
		ch <- ai.EventTextEnd{ContentIndex: idx, Text: text, Partial: partial}
		idx++
	}

	if turn.PartialText != "" {
		emitText(turn.PartialText)
	}
	if turn.Abort {
		partial.StopReason = ai.StopReasonAborted
		partial.ErrorMessage = "aborted by script"
		ch <- ai.EventError{Reason: ai.StopReasonAborted, Message: partial}
		return
	}
	if turn.Err != "" {
		partial.StopReason = ai.StopReasonError
		partial.ErrorMessage = turn.Err
		ch <- ai.EventError{Reason: ai.StopReasonError, Message: partial}
		return
	}

	if turn.Text != "" {
		emitText(turn.Text)
	}
	for _, tc := range turn.ToolCalls {
		args := tc.Args
		if args == "" {
			args = "{}"
		}
		ch <- ai.EventToolCallStart{ContentIndex: idx, ID: tc.ID, Name: tc.Name, Partial: partial}
		call := ai.ToolCall{ID: tc.ID, Name: tc.Name, Args: json.RawMessage(args)}
		partial.Content = append(partial.Content, call)
		ch <- ai.EventToolCallEnd{ContentIndex: idx, ToolCall: call, Partial: partial}
		idx++
	}

	reason := ai.StopReasonStop
	if len(turn.ToolCalls) > 0 {
		reason = ai.StopReasonToolUse
	}
	partial.StopReason = reason
	ch <- ai.EventDone{Reason: reason, Message: partial}
}
