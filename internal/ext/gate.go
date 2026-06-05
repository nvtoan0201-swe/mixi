package ext

import (
	"context"
	"encoding/json"

	"github.com/user/mixi-agent/internal/agent"
	"github.com/user/mixi-agent/internal/ai"
)

// FilterToolCall is the blocking tool_call gate: every subscribed, Ready
// extension sees the call sequentially in deterministic order; the first
// block wins and argument mutations chain. Extension failures fail OPEN
// (the call proceeds) — only the permission engine may fail closed, so the
// returned error is always nil.
func (h *Host) FilterToolCall(ctx context.Context, call ai.ToolCall) (json.RawMessage, *agent.BlockDecision, error) {
	args := call.Args
	mutated := false
	for _, name := range h.order {
		e := h.exts[name]
		if e.getState() != StateReady || !e.subscribed(EvToolCallGate) {
			continue
		}
		payload, err := json.Marshal(map[string]any{
			"callId": call.ID, "tool": call.Name, "args": json.RawMessage(args),
		})
		if err != nil {
			continue
		}
		resp, answered, err := e.deliverBlocking(ctx, EvToolCallGate, payload, h.blockTimeout)
		if err != nil {
			if ctx.Err() != nil {
				return nil, nil, nil // run aborted; nothing to gate
			}
			h.log.Warn("ext: gate failed open (extension gone)", "ext", name, "tool", call.Name, "err", err)
			continue
		}
		if !answered {
			h.log.Warn("ext: gate response timeout, treating as no-op", "ext", name, "tool", call.Name)
			if e.strikeBlocking() {
				h.log.Warn("ext: demoted to non-blocking delivery after repeated gate timeouts", "ext", name)
				h.notify("Extension \"" + name + "\" stopped answering tool gates and was demoted to non-blocking delivery.")
			}
			continue
		}
		if resp == nil {
			continue
		}
		if resp.Block != nil {
			reason := resp.Block.Reason
			if reason == "" {
				reason = "blocked by extension " + name
			}
			return nil, &agent.BlockDecision{Reason: "Blocked by extension \"" + name + "\": " + reason}, nil
		}
		if resp.Mutate != nil && len(resp.Mutate.Args) > 0 {
			args = resp.Mutate.Args
			mutated = true
		}
	}
	if mutated {
		return args, nil, nil
	}
	return nil, nil, nil
}
