package ai

import "testing"

func TestEnumWireValues(t *testing.T) {
	cases := []struct {
		got  string
		want string
	}{
		{string(RoleUser), "user"},
		{string(RoleAssistant), "assistant"},
		{string(RoleToolResult), "toolResult"},
		{string(StopReasonStop), "stop"},
		{string(StopReasonLength), "length"},
		{string(StopReasonToolUse), "toolUse"},
		{string(StopReasonError), "error"},
		{string(StopReasonAborted), "aborted"},
		{string(ThinkingOff), "off"},
		{string(ThinkingLow), "low"},
		{string(ThinkingMedium), "medium"},
		{string(ThinkingHigh), "high"},
		{string(CacheNone), "none"},
		{string(CacheShort), "short"},
		{string(CacheLong), "long"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("enum value = %q, want %q", c.got, c.want)
		}
	}
}

func TestMessageRoleAndTimestamp(t *testing.T) {
	msgs := []struct {
		m    Message
		role Role
		ts   int64
	}{
		{UserMessage{Timestamp: 1}, RoleUser, 1},
		{AssistantMessage{Timestamp: 2}, RoleAssistant, 2},
		{ToolResultMessage{Timestamp: 3}, RoleToolResult, 3},
	}
	for _, c := range msgs {
		if c.m.MsgRole() != c.role {
			t.Errorf("%T MsgRole = %q, want %q", c.m, c.m.MsgRole(), c.role)
		}
		if c.m.MsgTimestamp() != c.ts {
			t.Errorf("%T MsgTimestamp = %d, want %d", c.m, c.m.MsgTimestamp(), c.ts)
		}
	}
}

// Compile-time exhaustiveness: every union member satisfies its interface.
var (
	_ Content = TextContent{}
	_ Content = ImageContent{}
	_ Content = ThinkingContent{}
	_ Content = ToolCall{}

	_ Message = UserMessage{}
	_ Message = AssistantMessage{}
	_ Message = ToolResultMessage{}

	_ StreamEvent = EventStart{}
	_ StreamEvent = EventTextStart{}
	_ StreamEvent = EventTextDelta{}
	_ StreamEvent = EventTextEnd{}
	_ StreamEvent = EventThinkingStart{}
	_ StreamEvent = EventThinkingDelta{}
	_ StreamEvent = EventThinkingEnd{}
	_ StreamEvent = EventToolCallStart{}
	_ StreamEvent = EventToolCallDelta{}
	_ StreamEvent = EventToolCallEnd{}
	_ StreamEvent = EventDone{}
	_ StreamEvent = EventError{}
)
