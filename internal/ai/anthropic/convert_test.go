package anthropic

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/user/mixi-agent/internal/ai"
)

func TestConvertMergesConsecutiveToolResults(t *testing.T) {
	msgs, err := convertMessages([]ai.Message{
		ai.AssistantMessage{Content: []ai.Content{
			ai.ToolCall{ID: "t1", Name: "grep", Args: json.RawMessage(`{"a":1}`)},
			ai.ToolCall{ID: "t2", Name: "read", Args: json.RawMessage(`{"b":2}`)},
		}},
		ai.ToolResultMessage{ToolCallID: "t1", Content: []ai.Content{ai.TextContent{Text: "r1"}}},
		ai.ToolResultMessage{ToolCallID: "t2", Content: []ai.Content{ai.TextContent{Text: "r2"}}, IsError: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 {
		t.Fatalf("wire messages = %d, want 2 (assistant + merged user)", len(msgs))
	}
	user := msgs[1]
	if user.Role != "user" || len(user.Content) != 2 {
		t.Fatalf("merged tool results = %+v", user)
	}
	if user.Content[0].ToolUseID != "t1" || user.Content[1].ToolUseID != "t2" {
		t.Error("tool_use_id pairing broken")
	}
	if !user.Content[1].IsError {
		t.Error("is_error not propagated")
	}
}

func TestConvertSynthesizesOrphanedToolResult(t *testing.T) {
	msgs, err := convertMessages([]ai.Message{
		ai.AssistantMessage{Content: []ai.Content{
			ai.ToolCall{ID: "t1", Name: "grep", Args: json.RawMessage(`{}`)},
			ai.ToolCall{ID: "t2", Name: "read", Args: json.RawMessage(`{}`)},
		}},
		ai.ToolResultMessage{ToolCallID: "t1", Content: []ai.Content{ai.TextContent{Text: "ok"}}},
		ai.UserMessage{Content: []ai.Content{ai.TextContent{Text: "continue"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	// assistant, merged results (t1 real + t2 synthetic), trailing user
	if len(msgs) != 3 {
		t.Fatalf("wire messages = %d, want 3", len(msgs))
	}
	results := msgs[1].Content
	if len(results) != 2 {
		t.Fatalf("results = %d, want 2 (one synthetic)", len(results))
	}
	synth := results[1]
	if synth.Type != "tool_result" || synth.ToolUseID != "t2" || len(synth.Content) != 0 {
		t.Errorf("synthetic empty tool_result malformed: %+v", synth)
	}
}

func TestConvertOrphanAtEndOfHistory(t *testing.T) {
	msgs, err := convertMessages([]ai.Message{
		ai.AssistantMessage{Content: []ai.Content{
			ai.ToolCall{ID: "t1", Name: "grep", Args: json.RawMessage(`{}`)},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 || msgs[1].Content[0].ToolUseID != "t1" {
		t.Fatalf("trailing orphan tool_use must get a synthetic result: %+v", msgs)
	}
}

func TestConvertThinkingReplayRules(t *testing.T) {
	blocks, _ := convertAssistantContent([]ai.Content{
		ai.ThinkingContent{Thinking: "signed", Signature: "sig123"},
		ai.ThinkingContent{Thinking: "[Reasoning redacted]", Signature: "encdata", Redacted: true},
		ai.ThinkingContent{Thinking: "unsigned reasoning"},
		ai.TextContent{Text: "answer"},
	})
	want := []wireBlock{
		{Type: "thinking", Thinking: "signed", Signature: "sig123"},
		{Type: "redacted_thinking", Data: "encdata"},
		{Type: "text", Text: "unsigned reasoning"}, // signature-less → text downgrade
		{Type: "text", Text: "answer"},
	}
	if !reflect.DeepEqual(blocks, want) {
		t.Errorf("thinking replay mismatch\ngot:  %+v\nwant: %+v", blocks, want)
	}
}

func TestConvertImagesAndEmptyText(t *testing.T) {
	blocks, err := convertUserContent([]ai.Content{
		ai.TextContent{Text: ""}, // empty text dropped (API rejects it)
		ai.ImageContent{Data: "aWco", MimeType: "image/png"},
		ai.TextContent{Text: "see image"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []wireBlock{
		{Type: "image", Source: &imageSource{Type: "base64", MediaType: "image/png", Data: "aWco"}},
		{Type: "text", Text: "see image"},
	}
	if !reflect.DeepEqual(blocks, want) {
		t.Errorf("user content mismatch\ngot:  %+v\nwant: %+v", blocks, want)
	}
}

func TestConvertSkipsEmptyAssistantTurn(t *testing.T) {
	msgs, err := convertMessages([]ai.Message{
		ai.UserMessage{Content: []ai.Content{ai.TextContent{Text: "hi"}}},
		ai.AssistantMessage{}, // aborted turn with no content
		ai.UserMessage{Content: []ai.Content{ai.TextContent{Text: "again"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 {
		t.Fatalf("wire messages = %d, want 2 (empty assistant dropped)", len(msgs))
	}
}

func TestConvertEmptyToolArgsDefaultToObject(t *testing.T) {
	blocks, _ := convertAssistantContent([]ai.Content{
		ai.ToolCall{ID: "t1", Name: "noop"},
	})
	if string(blocks[0].Input) != "{}" {
		t.Errorf("empty args = %s, want {}", blocks[0].Input)
	}
}
