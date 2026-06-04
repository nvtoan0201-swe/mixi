package modes

import (
	"encoding/json"
	"io"
	"sync"

	"github.com/user/mixi-agent/internal/agent"
	"github.com/user/mixi-agent/internal/ai"
)

// JSONSink streams every agent event as one JSON line. Messages reuse the
// ai package's discriminated wire encoding; stream deltas are flattened to
// small records (the bulky Partial snapshots are omitted — consumers
// reconstruct state from deltas plus the message_end line).
type JSONSink struct {
	mu  sync.Mutex
	enc *json.Encoder
}

func NewJSONSink(out io.Writer) *JSONSink {
	return &JSONSink{enc: json.NewEncoder(out)}
}

func (s *JSONSink) Emit(ev agent.Event) {
	rec := eventRecord(ev)
	if rec == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Encode errors (closed pipe) are unrecoverable mid-stream; drop quietly
	// rather than corrupting the remaining lines with error text.
	_ = s.enc.Encode(rec)
}

// eventRecord flattens one agent event into an ordered-field wire map.
func eventRecord(ev agent.Event) map[string]any {
	switch e := ev.(type) {
	case agent.EvAgentStart:
		return rec("agent_start")
	case agent.EvAgentEnd:
		return rec("agent_end", "reason", string(e.Reason))
	case agent.EvTurnStart:
		return rec("turn_start", "turn", e.Turn)
	case agent.EvTurnEnd:
		return rec("turn_end", "turn", e.Turn)
	case agent.EvMessageStart:
		return rec("message_start", "message", agentMessageJSON(e.Msg))
	case agent.EvMessageEnd:
		return rec("message_end", "message", agentMessageJSON(e.Msg))
	case agent.EvMessageUpdate:
		return streamRecord(e.StreamEvent)
	case agent.EvToolStart:
		return rec("tool_start", "callId", e.Call.ID, "tool", e.Call.Name, "args", json.RawMessage(e.Call.Args))
	case agent.EvToolUpdate:
		return rec("tool_update", "callId", e.CallID, "partial", e.Partial)
	case agent.EvToolEnd:
		return rec("tool_end", "callId", e.CallID, "result", messageJSON(e.Result))
	case agent.EvRetryStart:
		return rec("retry_start", "attempt", e.Attempt, "max", e.Max, "delayMs", e.Delay.Milliseconds(), "error", e.Err)
	case agent.EvRetryEnd:
		return rec("retry_end", "ok", e.OK, "attempt", e.Attempt)
	case agent.EvNotice:
		return rec("notice", "text", e.Text)
	case agent.EvCompactionStart:
		return rec("compaction_start")
	case agent.EvCompactionEnd:
		return rec("compaction_end", "tokensBefore", e.TokensBefore)
	default:
		return nil // EvPermissionAsk etc. — no wire form until their phase
	}
}

// streamRecord flattens a provider stream event (render-only deltas).
func streamRecord(ev ai.StreamEvent) map[string]any {
	switch e := ev.(type) {
	case ai.EventStart:
		return rec("stream_start")
	case ai.EventTextStart:
		return rec("text_start", "index", e.ContentIndex)
	case ai.EventTextDelta:
		return rec("text_delta", "index", e.ContentIndex, "delta", e.Delta)
	case ai.EventTextEnd:
		return rec("text_end", "index", e.ContentIndex, "text", e.Text)
	case ai.EventThinkingStart:
		return rec("thinking_start", "index", e.ContentIndex)
	case ai.EventThinkingDelta:
		return rec("thinking_delta", "index", e.ContentIndex, "delta", e.Delta)
	case ai.EventThinkingEnd:
		return rec("thinking_end", "index", e.ContentIndex)
	case ai.EventToolCallStart:
		return rec("tool_call_start", "index", e.ContentIndex, "callId", e.ID, "tool", e.Name)
	case ai.EventToolCallDelta:
		return rec("tool_call_delta", "index", e.ContentIndex, "delta", e.Delta)
	case ai.EventToolCallEnd:
		return rec("tool_call_end", "index", e.ContentIndex, "callId", e.ToolCall.ID)
	case ai.EventDone:
		return rec("stream_done", "reason", string(e.Reason))
	case ai.EventError:
		return rec("stream_error", "reason", string(e.Reason), "error", e.Message.ErrorMessage)
	default:
		return nil
	}
}

// rec builds {"event": name, k1: v1, ...}.
func rec(name string, kv ...any) map[string]any {
	m := map[string]any{"event": name}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i].(string)] = kv[i+1]
	}
	return m
}

// messageJSON encodes an ai.Message with its role discriminator; encoding
// problems surface inline so the JSONL stream never silently drops a line.
func messageJSON(m ai.Message) json.RawMessage {
	raw, err := ai.MarshalMessage(m)
	if err != nil {
		raw, _ = json.Marshal(map[string]string{"encodeError": err.Error()})
	}
	return raw
}

// agentMessageJSON encodes the AgentMessage union: model messages reuse the
// ai wire form under a kind tag; harness items get their natural fields.
func agentMessageJSON(m agent.AgentMessage) map[string]any {
	switch v := m.(type) {
	case agent.ModelMessage:
		return map[string]any{"kind": "model", "message": messageJSON(v.Msg)}
	case agent.BashExecution:
		return map[string]any{"kind": "bashExecution", "command": v.Command, "exitCode": v.ExitCode}
	case agent.CompactionSummary:
		return map[string]any{"kind": "compaction", "summary": v.Summary}
	case agent.BranchSummary:
		return map[string]any{"kind": "branchSummary", "summary": v.Summary}
	case agent.CustomMessage:
		return map[string]any{"kind": "custom", "content": v.Content}
	default:
		return map[string]any{"kind": "unknown"}
	}
}
