package compact

import (
	"encoding/json"
	"testing"

	"github.com/user/mixi-agent/internal/agent"
	"github.com/user/mixi-agent/internal/ai"
)

func text(n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = 'x'
	}
	return string(b)
}

func userMsg(chars int) agent.AgentMessage {
	return agent.ModelMessage{Msg: ai.UserMessage{Content: []ai.Content{ai.TextContent{Text: text(chars)}}}}
}

func assistantMsg(chars, usageTotal int) agent.AgentMessage {
	return agent.ModelMessage{Msg: ai.AssistantMessage{
		Content: []ai.Content{ai.TextContent{Text: text(chars)}},
		Usage:   ai.Usage{Total: usageTotal},
	}}
}

func toolResultMsg(chars int) agent.AgentMessage {
	return agent.ModelMessage{Msg: ai.ToolResultMessage{Content: []ai.Content{ai.TextContent{Text: text(chars)}}}}
}

func TestEstimateMessageRoles(t *testing.T) {
	cases := []struct {
		name string
		msg  ai.Message
		want int
	}{
		{"user text", ai.UserMessage{Content: []ai.Content{ai.TextContent{Text: text(100)}}}, 25},
		{"user rounds up", ai.UserMessage{Content: []ai.Content{ai.TextContent{Text: text(101)}}}, 26},
		{"assistant text+thinking", ai.AssistantMessage{Content: []ai.Content{
			ai.ThinkingContent{Thinking: text(40)},
			ai.TextContent{Text: text(60)},
		}}, 25},
		{"assistant tool args", ai.AssistantMessage{Content: []ai.Content{
			ai.ToolCall{Name: "read", Args: json.RawMessage(`{"path":"a.go"}`)}, // 4 + 15 chars
		}}, 5},
		{"tool result", ai.ToolResultMessage{Content: []ai.Content{ai.TextContent{Text: text(200)}}}, 50},
		{"image data counts", ai.UserMessage{Content: []ai.Content{ai.ImageContent{Data: text(400)}}}, 100},
		{"empty", ai.UserMessage{}, 0},
	}
	for _, tc := range cases {
		if got := EstimateMessage(tc.msg); got != tc.want {
			t.Errorf("%s: got %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestEstimateAgentMessageHarnessItems(t *testing.T) {
	cases := []struct {
		name string
		msg  agent.AgentMessage
		want int
	}{
		{"bash execution", agent.BashExecution{Command: text(20), Output: text(80)}, 25},
		{"compaction summary", agent.CompactionSummary{Summary: text(40)}, 10},
		{"branch summary", agent.BranchSummary{Summary: text(40)}, 10},
		{"custom message", agent.CustomMessage{Content: text(40)}, 10},
		{"excluded tool result", agent.ModelMessage{
			Msg:      ai.ToolResultMessage{Content: []ai.Content{ai.TextContent{Text: text(400)}}},
			Excluded: true,
		}, 0},
	}
	for _, tc := range cases {
		if got := EstimateAgentMessage(tc.msg); got != tc.want {
			t.Errorf("%s: got %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestEstimateContextAnchored(t *testing.T) {
	msgs := []agent.AgentMessage{
		userMsg(4000),          // before anchor: ignored
		assistantMsg(400, 0),   // no usage: not an anchor
		assistantMsg(40, 5000), // anchor
		toolResultMsg(400),     // +100
		userMsg(200),           // +50
	}
	if got := EstimateContext(msgs); got != 5150 {
		t.Fatalf("anchored estimate = %d, want 5150", got)
	}
}

func TestEstimateContextNoAnchor(t *testing.T) {
	msgs := []agent.AgentMessage{
		userMsg(400),         // 100
		assistantMsg(200, 0), // 50 — usage absent, pure heuristic
		toolResultMsg(100),   // 25
	}
	if got := EstimateContext(msgs); got != 175 {
		t.Fatalf("unanchored estimate = %d, want 175", got)
	}
}

func TestEstimateContextAnchorIsLast(t *testing.T) {
	msgs := []agent.AgentMessage{
		userMsg(400),
		assistantMsg(40, 9000), // anchor is the final message: nothing trails
	}
	if got := EstimateContext(msgs); got != 9000 {
		t.Fatalf("estimate = %d, want 9000", got)
	}
}
