package ext

import (
	"encoding/json"
	"strings"

	"github.com/user/mixi-agent/internal/agent"
	"github.com/user/mixi-agent/internal/ai"
)

// Event names deliverable to extensions: the agent runtime events plus the
// host-synthesized session markers. tool_call is the blocking gate.
const (
	EvSessionStart    = "session_start"
	EvSessionShutdown = "session_shutdown"
	EvToolCallGate    = "tool_call"
	EvUserInput       = "user_input"
)

// knownEvents validates hello subscriptions; unknown names are ignored
// with a WARN so a typo surfaces instead of silently never firing.
var knownEvents = map[string]bool{
	"agent_start": true, "agent_end": true,
	"turn_start": true, "turn_end": true,
	"message_start": true, "message_update": true, "message_end": true,
	"tool_start": true, "tool_update": true, "tool_result": true,
	"retry_start": true, "retry_end": true,
	"compaction_start": true, "compaction_end": true,
	"permission_ask": true, "notice": true,
	EvSessionStart: true, EvSessionShutdown: true,
	EvToolCallGate: true, EvUserInput: true,
}

// hostEvent is one named, JSON-ready event for fan-out.
type hostEvent struct {
	name    string
	payload json.RawMessage
}

func mkEvent(name string, payload any) hostEvent {
	b, err := json.Marshal(payload)
	if err != nil {
		b = []byte("{}")
	}
	return hostEvent{name: name, payload: b}
}

// busEvents projects one agent bus event into extension events. A user
// message start additionally yields user_input.
func busEvents(ev agent.Event) []hostEvent {
	switch v := ev.(type) {
	case agent.EvAgentStart:
		return []hostEvent{mkEvent("agent_start", struct{}{})}
	case agent.EvAgentEnd:
		return []hostEvent{mkEvent("agent_end", map[string]any{"reason": v.Reason})}
	case agent.EvTurnStart:
		return []hostEvent{mkEvent("turn_start", map[string]any{"turn": v.Turn})}
	case agent.EvTurnEnd:
		return []hostEvent{mkEvent("turn_end", map[string]any{"turn": v.Turn})}
	case agent.EvMessageStart:
		out := []hostEvent{mkEvent("message_start", map[string]any{"role": messageRole(v.Msg)})}
		if text, ok := userText(v.Msg); ok {
			out = append(out, mkEvent(EvUserInput, map[string]any{"text": text}))
		}
		return out
	case agent.EvMessageEnd:
		return []hostEvent{mkEvent("message_end", map[string]any{"role": messageRole(v.Msg)})}
	case agent.EvMessageUpdate:
		return []hostEvent{mkEvent("message_update", struct{}{})}
	case agent.EvToolStart:
		return []hostEvent{mkEvent("tool_start", map[string]any{
			"callId": v.Call.ID, "tool": v.Call.Name, "args": json.RawMessage(v.Call.Args)})}
	case agent.EvToolUpdate:
		return []hostEvent{mkEvent("tool_update", map[string]any{"callId": v.CallID})}
	case agent.EvToolEnd:
		return []hostEvent{mkEvent("tool_result", map[string]any{
			"callId": v.CallID, "tool": v.Result.ToolName, "isError": v.Result.IsError})}
	case agent.EvRetryStart:
		return []hostEvent{mkEvent("retry_start", map[string]any{
			"attempt": v.Attempt, "max": v.Max, "err": v.Err})}
	case agent.EvRetryEnd:
		return []hostEvent{mkEvent("retry_end", map[string]any{"ok": v.OK, "attempt": v.Attempt})}
	case agent.EvCompactionStart:
		return []hostEvent{mkEvent("compaction_start", struct{}{})}
	case agent.EvCompactionEnd:
		return []hostEvent{mkEvent("compaction_end", map[string]any{"tokensBefore": v.TokensBefore})}
	case agent.EvPermissionAsk:
		return []hostEvent{mkEvent("permission_ask", struct{}{})}
	case agent.EvNotice:
		return []hostEvent{mkEvent("notice", map[string]any{"text": v.Text})}
	}
	return nil
}

// messageRole names the message kind for event payloads.
func messageRole(m agent.AgentMessage) string {
	switch v := m.(type) {
	case agent.ModelMessage:
		return string(v.Msg.MsgRole())
	case agent.BashExecution:
		return "bash"
	case agent.CompactionSummary:
		return "compaction"
	case agent.BranchSummary:
		return "branch"
	case agent.CustomMessage:
		return "custom"
	}
	return "unknown"
}

// userText extracts the text of a user-authored message, reporting whether
// m is one.
func userText(m agent.AgentMessage) (string, bool) {
	mm, ok := m.(agent.ModelMessage)
	if !ok {
		return "", false
	}
	um, ok := mm.Msg.(ai.UserMessage)
	if !ok {
		return "", false
	}
	var sb strings.Builder
	for _, c := range um.Content {
		if t, ok := c.(ai.TextContent); ok {
			sb.WriteString(t.Text)
		}
	}
	return sb.String(), true
}
