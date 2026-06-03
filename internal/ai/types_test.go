package ai_test

import (
	"encoding/json"
	"testing"

	"github.com/user/mixi-agent/internal/ai"
)

// jsonField unmarshals data and returns the value at key as a string.
func jsonField(t *testing.T, data []byte, key string) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	v, ok := m[key]
	if !ok {
		t.Fatalf("field %q not found in %s", key, data)
	}
	s, _ := v.(string)
	return s
}

func jsonHasKey(t *testing.T, data []byte, key string) bool {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	_, ok := m[key]
	return ok
}

// ── Content block serialization ───────────────────────────────────────────────

func TestTextContent_Marshal(t *testing.T) {
	tc := ai.TextContent{Type: "text", Text: "hello world"}
	data, err := json.Marshal(tc)
	if err != nil {
		t.Fatal(err)
	}
	if got := jsonField(t, data, "type"); got != "text" {
		t.Errorf("type: got %q, want %q", got, "text")
	}
	if got := jsonField(t, data, "text"); got != "hello world" {
		t.Errorf("text: got %q, want %q", got, "hello world")
	}
}

func TestImageContent_Marshal(t *testing.T) {
	ic := ai.ImageContent{Type: "image", MediaType: "image/png", Data: "base64data"}
	data, err := json.Marshal(ic)
	if err != nil {
		t.Fatal(err)
	}
	if got := jsonField(t, data, "type"); got != "image" {
		t.Errorf("type: got %q, want %q", got, "image")
	}
	if got := jsonField(t, data, "mediaType"); got != "image/png" {
		t.Errorf("mediaType: got %q, want %q", got, "image/png")
	}
}

func TestThinkingContent_Marshal(t *testing.T) {
	tc := ai.ThinkingContent{Type: "thinking", Thinking: "I think therefore I am", Signature: "sig123"}
	data, err := json.Marshal(tc)
	if err != nil {
		t.Fatal(err)
	}
	if got := jsonField(t, data, "type"); got != "thinking" {
		t.Errorf("type: got %q, want %q", got, "thinking")
	}
	if got := jsonField(t, data, "thinking"); got != "I think therefore I am" {
		t.Errorf("thinking: got %q", got)
	}
	if got := jsonField(t, data, "signature"); got != "sig123" {
		t.Errorf("signature: got %q, want %q", got, "sig123")
	}
}

func TestToolCall_Marshal(t *testing.T) {
	tc := ai.ToolCall{
		ID:   "tool-1",
		Name: "read_file",
		Args: json.RawMessage(`{"path":"/etc/hosts"}`),
	}
	data, err := json.Marshal(tc)
	if err != nil {
		t.Fatal(err)
	}
	if got := jsonField(t, data, "id"); got != "tool-1" {
		t.Errorf("id: got %q, want %q", got, "tool-1")
	}
	if got := jsonField(t, data, "name"); got != "read_file" {
		t.Errorf("name: got %q, want %q", got, "read_file")
	}
	if !jsonHasKey(t, data, "args") {
		t.Error("args field missing")
	}
}

// ── Message serialization ─────────────────────────────────────────────────────

func TestUserMessage_Marshal(t *testing.T) {
	msg := ai.UserMessage{
		Role:      "user",
		Content:   []ai.ContentBlock{ai.TextContent{Type: "text", Text: "hello"}},
		Timestamp: 1234567890,
	}
	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	if got := jsonField(t, data, "role"); got != "user" {
		t.Errorf("role: got %q, want %q", got, "user")
	}
	if !jsonHasKey(t, data, "content") {
		t.Error("content field missing")
	}
	if !jsonHasKey(t, data, "timestamp") {
		t.Error("timestamp field missing")
	}
}

func TestAssistantMessage_Marshal(t *testing.T) {
	msg := ai.AssistantMessage{
		Role:       "assistant",
		Content:    []ai.ContentBlock{ai.TextContent{Type: "text", Text: "hi"}},
		Model:      "claude-sonnet-4-6",
		API:        "anthropic-messages",
		StopReason: ai.StopReasonStop,
		Usage:      ai.Usage{InputTokens: 10, OutputTokens: 5},
	}
	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	if got := jsonField(t, data, "role"); got != "assistant" {
		t.Errorf("role: got %q", got)
	}
	if got := jsonField(t, data, "model"); got != "claude-sonnet-4-6" {
		t.Errorf("model: got %q", got)
	}
	if got := jsonField(t, data, "stopReason"); got != "stop" {
		t.Errorf("stopReason: got %q, want %q", got, "stop")
	}
	// errorMessage is omitempty — should be absent when empty
	if jsonHasKey(t, data, "errorMessage") {
		t.Error("errorMessage should be omitted when empty")
	}
}

func TestToolResultMessage_Marshal(t *testing.T) {
	msg := ai.ToolResultMessage{
		Role:       "toolResult",
		ToolCallID: "tool-1",
		Content:    []ai.ContentBlock{ai.TextContent{Type: "text", Text: "ok"}},
		IsError:    false,
		Timestamp:  1000,
	}
	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	if got := jsonField(t, data, "role"); got != "toolResult" {
		t.Errorf("role: got %q", got)
	}
	if got := jsonField(t, data, "toolCallId"); got != "tool-1" {
		t.Errorf("toolCallId: got %q", got)
	}
}

// ── Enum values ───────────────────────────────────────────────────────────────

func TestStopReason_Values(t *testing.T) {
	cases := []struct {
		r    ai.StopReason
		want string
	}{
		{ai.StopReasonStop, "stop"},
		{ai.StopReasonLength, "length"},
		{ai.StopReasonToolUse, "toolUse"},
		{ai.StopReasonError, "error"},
		{ai.StopReasonAborted, "aborted"},
	}
	for _, c := range cases {
		if string(c.r) != c.want {
			t.Errorf("StopReason: got %q, want %q", c.r, c.want)
		}
	}
}

func TestThinkingLevel_Values(t *testing.T) {
	cases := []struct {
		l    ai.ThinkingLevel
		want string
	}{
		{ai.ThinkingOff, "off"},
		{ai.ThinkingLow, "low"},
		{ai.ThinkingMedium, "medium"},
		{ai.ThinkingHigh, "high"},
	}
	for _, c := range cases {
		if string(c.l) != c.want {
			t.Errorf("ThinkingLevel: got %q, want %q", c.l, c.want)
		}
	}
}

// ── StreamOptions: APIKey must not be serialized ──────────────────────────────

func TestStreamOptions_APIKeyNotSerialized(t *testing.T) {
	opts := ai.StreamOptions{APIKey: "sk-secret"}
	data, err := json.Marshal(opts)
	if err != nil {
		t.Fatal(err)
	}
	if jsonHasKey(t, data, "APIKey") || jsonHasKey(t, data, "apiKey") {
		t.Error("APIKey must not appear in serialized StreamOptions")
	}
	if string(data) != "{}" {
		// temperature and maxTokens are omitempty, so empty opts → {}
		t.Logf("StreamOptions JSON: %s", data)
	}
}

// ── Model serialization ───────────────────────────────────────────────────────

func TestModel_Marshal(t *testing.T) {
	m := ai.Model{
		API:           "anthropic-messages",
		Name:          "claude-sonnet-4-6",
		ContextWindow: 200000,
		MaxOutput:     16000,
		Thinking:      ai.ThinkingOff,
		Capabilities:  ai.ModelCaps{Vision: true, Tools: true},
	}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if got := jsonField(t, data, "api"); got != "anthropic-messages" {
		t.Errorf("api: got %q", got)
	}
	if got := jsonField(t, data, "name"); got != "claude-sonnet-4-6" {
		t.Errorf("name: got %q", got)
	}
}
