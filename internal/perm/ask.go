package perm

import (
	"context"
	"encoding/json"
)

// AskDecision is the user's answer to a permission prompt.
type AskDecision int

const (
	AskDeny AskDecision = iota
	AskAllow
	AskAlways // allow and record a session grant
)

// AskRequest is everything a UI needs to render an approval prompt.
type AskRequest struct {
	Tool    string
	Args    json.RawMessage
	Preview string // what the call would do (diff, command, args)
	Why     string // why approval is needed
}

// Asker resolves ASK decisions. Implementations: TUI modal, RPC
// request/response, and the headless fallback below.
type Asker interface {
	// Ask blocks until the user decides (or ctx is cancelled).
	Ask(ctx context.Context, req AskRequest) (AskDecision, error)
}

// PendingAsk pairs a request with the channel its answer travels back on.
// Interactive frontends (TUI modal, RPC forwarder) receive it as an event
// payload and send exactly one decision; Reply is buffered so the answer
// never blocks the UI.
type PendingAsk struct {
	Req   AskRequest
	Reply chan AskDecision
}

// NotifyAsker bridges Ask onto an event-driven frontend: each request is
// handed to Notify (e.g. published on the agent event bus) and the call
// blocks until the frontend answers or ctx is cancelled. Cancellation
// (run aborted, UI gone) denies — the safe default.
type NotifyAsker struct {
	Notify func(PendingAsk)
}

func (n NotifyAsker) Ask(ctx context.Context, req AskRequest) (AskDecision, error) {
	p := PendingAsk{Req: req, Reply: make(chan AskDecision, 1)}
	n.Notify(p)
	select {
	case d := <-p.Reply:
		return d, nil
	case <-ctx.Done():
		return AskDeny, ctx.Err()
	}
}

// HeadlessAsker answers for sessions with no one to ask (print mode).
// Default is deny; AllowAll is set when the user explicitly chose a
// permissive mode (yolo/auto-edit), turning every ask into an approval.
type HeadlessAsker struct {
	AllowAll bool
}

func (h HeadlessAsker) Ask(_ context.Context, _ AskRequest) (AskDecision, error) {
	if h.AllowAll {
		return AskAllow, nil
	}
	return AskDeny, nil
}
