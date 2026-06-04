package agent

import (
	"context"
	"log/slog"

	"github.com/user/mixi-agent/internal/ai"
	"github.com/user/mixi-agent/internal/tools"
)

// BlockDecision is returned by BeforeToolCall to veto a call; Reason is
// surfaced to the model as an IsError tool result.
type BlockDecision struct {
	Reason string
}

// TurnUpdate lets PrepareNextTurn swap the model or thinking level for the
// next turn. Nil fields keep the current value.
type TurnUpdate struct {
	Model    *ai.Model
	Thinking *ai.ThinkingLevel
}

// Hooks customizes the loop. All fields are optional; nil means no-op.
// Hook errors are logged and treated as no-op — they never abort the run.
type Hooks struct {
	// TransformContext rewrites the message list before each LLM call
	// (context pruning, working-set injection).
	TransformContext func(ctx context.Context, msgs []AgentMessage) ([]AgentMessage, error)
	// GetAPIKey resolves the key per stream call (rotation-friendly).
	GetAPIKey func(provider string) (string, error)
	// BeforeToolCall runs after validation, before execution; non-nil
	// BlockDecision turns the call into an error result.
	BeforeToolCall func(ctx context.Context, c ai.ToolCall) (*BlockDecision, error)
	// AfterToolCall runs after execution and may mutate the result.
	AfterToolCall func(ctx context.Context, c ai.ToolCall, r *tools.ToolResult) error
	// ShouldStop ends the run after a turn when it returns true.
	ShouldStop func(turn int, last *ai.AssistantMessage) bool
	// PrepareNextTurn may swap model/thinking before the next turn.
	PrepareNextTurn func(turn int) *TurnUpdate
}

// transformContext applies the hook, falling back to the input on error.
func (h Hooks) transformContext(ctx context.Context, log *slog.Logger, msgs []AgentMessage) []AgentMessage {
	if h.TransformContext == nil {
		return msgs
	}
	out, err := h.TransformContext(ctx, msgs)
	if err != nil {
		log.Warn("agent: TransformContext hook failed, ignoring", "err", err)
		return msgs
	}
	return out
}

// apiKey resolves the API key, returning "" on hook absence or error.
func (h Hooks) apiKey(log *slog.Logger, provider string) string {
	if h.GetAPIKey == nil {
		return ""
	}
	key, err := h.GetAPIKey(provider)
	if err != nil {
		log.Warn("agent: GetAPIKey hook failed, ignoring", "err", err)
		return ""
	}
	return key
}

// beforeToolCall returns the hook's block decision; hook errors are no-ops.
func (h Hooks) beforeToolCall(ctx context.Context, log *slog.Logger, c ai.ToolCall) *BlockDecision {
	if h.BeforeToolCall == nil {
		return nil
	}
	block, err := h.BeforeToolCall(ctx, c)
	if err != nil {
		log.Warn("agent: BeforeToolCall hook failed, ignoring", "err", err)
		return nil
	}
	return block
}

// afterToolCall lets the hook mutate r; hook errors are no-ops.
func (h Hooks) afterToolCall(ctx context.Context, log *slog.Logger, c ai.ToolCall, r *tools.ToolResult) {
	if h.AfterToolCall == nil {
		return
	}
	if err := h.AfterToolCall(ctx, c, r); err != nil {
		log.Warn("agent: AfterToolCall hook failed, ignoring", "err", err)
	}
}

// shouldStop reports whether the hook requests a stop after this turn.
func (h Hooks) shouldStop(turn int, last *ai.AssistantMessage) bool {
	if h.ShouldStop == nil {
		return false
	}
	return h.ShouldStop(turn, last)
}

// prepareNextTurn returns the hook's turn update, nil for no change.
func (h Hooks) prepareNextTurn(turn int) *TurnUpdate {
	if h.PrepareNextTurn == nil {
		return nil
	}
	return h.PrepareNextTurn(turn)
}
