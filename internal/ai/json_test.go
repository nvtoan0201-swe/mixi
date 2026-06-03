package ai

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestContentRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		in   Content
	}{
		{"text", TextContent{Text: "hello"}},
		{"text with signature", TextContent{Text: "hi", TextSignature: `{"v":1,"id":"t1"}`}},
		{"image", ImageContent{Data: "aGVsbG8=", MimeType: "image/png"}},
		{"thinking", ThinkingContent{Thinking: "hmm", Signature: "sig123"}},
		{"redacted thinking", ThinkingContent{Thinking: "[redacted]", Signature: "enc", Redacted: true}},
		{"tool call", ToolCall{ID: "toolu_01", Name: "grep", Args: json.RawMessage(`{"pattern":"x"}`)}},
		{"tool call with thought sig", ToolCall{ID: "t2", Name: "ls", Args: json.RawMessage(`{}`), ThoughtSignature: "ts"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b, err := MarshalContent(c.in)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			out, err := UnmarshalContent(b)
			if err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if !reflect.DeepEqual(c.in, out) {
				t.Errorf("round-trip mismatch:\n in: %#v\nout: %#v", c.in, out)
			}
		})
	}
}

func TestContentDiscriminatorShape(t *testing.T) {
	cases := []struct {
		in   Content
		want string
	}{
		{TextContent{Text: "hi"}, `{"type":"text","text":"hi"}`},
		{ImageContent{Data: "QQ==", MimeType: "image/png"}, `{"type":"image","data":"QQ==","mimeType":"image/png"}`},
		{ThinkingContent{Thinking: "x"}, `{"type":"thinking","thinking":"x"}`},
		{ToolCall{ID: "i", Name: "n", Args: json.RawMessage(`{}`)}, `{"type":"toolCall","id":"i","name":"n","args":{}}`},
	}
	for _, c := range cases {
		b, err := MarshalContent(c.in)
		if err != nil {
			t.Fatalf("marshal %T: %v", c.in, err)
		}
		if string(b) != c.want {
			t.Errorf("wire shape %T:\n got %s\nwant %s", c.in, b, c.want)
		}
	}
}

func TestMessageRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		in   Message
	}{
		{"user", UserMessage{
			Content:   []Content{TextContent{Text: "hi"}, ImageContent{Data: "QQ==", MimeType: "image/png"}},
			Timestamp: 1780470600000,
		}},
		{"assistant", AssistantMessage{
			Content: []Content{
				ThinkingContent{Thinking: "plan", Signature: "s"},
				TextContent{Text: "answer"},
				ToolCall{ID: "toolu_01", Name: "grep", Args: json.RawMessage(`{"pattern":"main"}`)},
			},
			API: "anthropic-messages", Provider: "anthropic", Model: "claude-sonnet-4-6",
			ResponseModel: "claude-sonnet-4-6", ResponseID: "msg_01",
			Usage: Usage{
				Input: 10, Output: 20, CacheRead: 5, CacheWrite: 3, Total: 38,
				Cost: Cost{Input: 0.001, Output: 0.002, CacheRead: 0.0001, CacheWrite: 0.0002, Total: 0.0033},
			},
			StopReason: StopReasonToolUse, Timestamp: 1780470604512,
		}},
		{"assistant error", AssistantMessage{
			Content: []Content{}, API: "anthropic-messages", Provider: "anthropic",
			Model: "claude-haiku-4-5", StopReason: StopReasonError,
			ErrorMessage: "overloaded", Timestamp: 2,
		}},
		{"tool result", ToolResultMessage{
			ToolCallID: "toolu_01", ToolName: "grep",
			Content:   []Content{TextContent{Text: "3 matches"}},
			Details:   json.RawMessage(`{"matches":3}`),
			IsError:   false,
			Timestamp: 1780470605000,
		}},
		{"tool result error", ToolResultMessage{
			ToolCallID: "toolu_02", ToolName: "bash",
			Content: []Content{TextContent{Text: "exit 1"}},
			IsError: true, Timestamp: 3,
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b, err := MarshalMessage(c.in)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			out, err := UnmarshalMessage(b)
			if err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if !reflect.DeepEqual(c.in, out) {
				t.Errorf("round-trip mismatch:\n in: %#v\nout: %#v", c.in, out)
			}
		})
	}
}

func TestMessageRoleDiscriminator(t *testing.T) {
	cases := []struct {
		m    Message
		role string
	}{
		{UserMessage{Content: []Content{TextContent{Text: "q"}}}, "user"},
		{AssistantMessage{Content: []Content{}}, "assistant"},
		{ToolResultMessage{Content: []Content{}}, "toolResult"},
	}
	for _, c := range cases {
		b, err := MarshalMessage(c.m)
		if err != nil {
			t.Fatalf("marshal %T: %v", c.m, err)
		}
		var probe struct {
			Role string `json:"role"`
		}
		if err := json.Unmarshal(b, &probe); err != nil {
			t.Fatalf("probe: %v", err)
		}
		if probe.Role != c.role {
			t.Errorf("%T role = %q, want %q", c.m, probe.Role, c.role)
		}
	}
}

func TestUnmarshalUnknownDiscriminators(t *testing.T) {
	if _, err := UnmarshalContent([]byte(`{"type":"bogus"}`)); err == nil || !strings.Contains(err.Error(), "unknown content type") {
		t.Errorf("unknown content type: got err=%v, want unknown-type error", err)
	}
	if _, err := UnmarshalContent([]byte(`not json`)); err == nil {
		t.Error("invalid content JSON: want error")
	}
	if _, err := UnmarshalMessage([]byte(`{"role":"bogus"}`)); err == nil || !strings.Contains(err.Error(), "unknown message role") {
		t.Errorf("unknown message role: got err=%v, want unknown-role error", err)
	}
	if _, err := UnmarshalMessage([]byte(`{`)); err == nil {
		t.Error("invalid message JSON: want error")
	}
}

func TestMessageRejectsBadNestedContent(t *testing.T) {
	in := []byte(`{"role":"user","content":[{"type":"mystery"}],"timestamp":1}`)
	if _, err := UnmarshalMessage(in); err == nil {
		t.Error("want error for unknown nested content type")
	}
}
