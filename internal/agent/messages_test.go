package agent

import (
	"strings"
	"testing"

	"github.com/user/mixi-agent/internal/ai"
)

func textOf(t *testing.T, m ai.Message) string {
	t.Helper()
	um, ok := m.(ai.UserMessage)
	if !ok {
		t.Fatalf("expected UserMessage, got %T", m)
	}
	tc, ok := um.Content[0].(ai.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", um.Content[0])
	}
	return tc.Text
}

func TestToLLMPassesModelMessagesThrough(t *testing.T) {
	asst := ai.AssistantMessage{Content: []ai.Content{ai.TextContent{Text: "reply"}}, Model: "m", StopReason: ai.StopReasonStop}
	out := ToLLM([]AgentMessage{
		ModelMessage{Msg: ai.UserMessage{Content: []ai.Content{ai.TextContent{Text: "hi"}}}},
		ModelMessage{Msg: asst},
	})
	if len(out) != 2 {
		t.Fatalf("len = %d", len(out))
	}
	if _, ok := out[1].(ai.AssistantMessage); !ok {
		t.Fatalf("assistant not preserved: %T", out[1])
	}
}

func TestToLLMDropsExcludedToolResults(t *testing.T) {
	tr := ai.ToolResultMessage{ToolCallID: "c1", ToolName: "read"}
	out := ToLLM([]AgentMessage{
		ModelMessage{Msg: tr, Excluded: true},
		ModelMessage{Msg: tr},
	})
	if len(out) != 1 {
		t.Fatalf("expected 1 message, got %d", len(out))
	}
}

func TestToLLMFormatsBashExecution(t *testing.T) {
	out := ToLLM([]AgentMessage{BashExecution{Command: "ls -la", Output: "total 0", ExitCode: 1}})
	text := textOf(t, out[0])
	for _, want := range []string{"ls -la", "total 0", "exit code 1"} {
		if !strings.Contains(text, want) {
			t.Fatalf("bash projection missing %q:\n%s", want, text)
		}
	}
}

func TestToLLMWrapsSummaries(t *testing.T) {
	out := ToLLM([]AgentMessage{
		CompactionSummary{Summary: "compacted stuff"},
		BranchSummary{Summary: "branch stuff"},
	})
	for i, want := range []string{"compacted stuff", "branch stuff"} {
		text := textOf(t, out[i])
		if !strings.HasPrefix(text, "<summary>") || !strings.HasSuffix(text, "</summary>") || !strings.Contains(text, want) {
			t.Fatalf("summary %d not wrapped: %q", i, text)
		}
	}
}

func TestToLLMDropsContentlessAssistantMessages(t *testing.T) {
	out := ToLLM([]AgentMessage{
		ModelMessage{Msg: ai.AssistantMessage{StopReason: ai.StopReasonError, ErrorMessage: "HTTP 500"}},
		ModelMessage{Msg: ai.AssistantMessage{Content: []ai.Content{ai.TextContent{Text: "kept"}}, StopReason: ai.StopReasonStop}},
	})
	if len(out) != 1 {
		t.Fatalf("expected only the non-empty assistant message, got %d", len(out))
	}
}

func TestToLLMProjectsCustomMessage(t *testing.T) {
	out := ToLLM([]AgentMessage{CustomMessage{Content: "extension says hi"}})
	if got := textOf(t, out[0]); got != "extension says hi" {
		t.Fatalf("custom = %q", got)
	}
}
