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
