package compact

import (
	"fmt"
	"strings"

	"github.com/user/mixi-agent/internal/ai"
	"github.com/user/mixi-agent/internal/session"
)

// toolResultMaxChars clips tool results in serialized conversations: the
// summarizer needs the gist of an output, not megabytes of it.
const toolResultMaxChars = 2000

// serializeEntries renders a window of session entries as plain text for the
// summarizer. Text form (not message form) keeps the summarizer from
// "continuing" the conversation instead of describing it.
func serializeEntries(entries []session.Entry) string {
	var b strings.Builder
	for _, e := range entries {
		switch v := e.(type) {
		case *session.MessageEntry:
			serializeMessage(&b, v.Message)
		case *session.CustomMessageEntry:
			writeBlock(&b, "[User]:", string(v.Content))
		case *session.BranchSummaryEntry:
			writeBlock(&b, "[Branch summary]:", v.Summary)
		}
		// Settings changes, labels, custom state entries carry no
		// conversational content worth summarizing.
	}
	return strings.TrimRight(b.String(), "\n")
}

func serializeMessage(b *strings.Builder, m ai.Message) {
	switch v := m.(type) {
	case ai.UserMessage:
		writeBlock(b, "[User]:", textOf(v.Content))
	case ai.AssistantMessage:
		if t := thinkingOf(v.Content); t != "" {
			writeBlock(b, "[Assistant thinking]:", t)
		}
		if t := textOf(v.Content); t != "" {
			writeBlock(b, "[Assistant]:", t)
		}
		if calls := toolCallsOf(v.Content); calls != "" {
			writeBlock(b, "[Assistant tool calls]:", calls)
		}
	case ai.ToolResultMessage:
		writeBlock(b, "[Tool result]:", clip(textOf(v.Content), toolResultMaxChars))
	}
}

func writeBlock(b *strings.Builder, label, body string) {
	b.WriteString(label)
	if body != "" {
		b.WriteByte(' ')
		b.WriteString(body)
	}
	b.WriteString("\n\n")
}

// textOf concatenates the text blocks of a content list; images render as a
// placeholder so the summarizer knows one was there.
func textOf(cs []ai.Content) string {
	var parts []string
	for _, c := range cs {
		switch v := c.(type) {
		case ai.TextContent:
			parts = append(parts, v.Text)
		case ai.ImageContent:
			parts = append(parts, "[image]")
		}
	}
	return strings.Join(parts, "\n")
}

func thinkingOf(cs []ai.Content) string {
	var parts []string
	for _, c := range cs {
		if v, ok := c.(ai.ThinkingContent); ok {
			parts = append(parts, v.Thinking)
		}
	}
	return strings.Join(parts, "\n")
}

// toolCallsOf renders calls as `name({...}); name({...})`.
func toolCallsOf(cs []ai.Content) string {
	var parts []string
	for _, c := range cs {
		if v, ok := c.(ai.ToolCall); ok {
			parts = append(parts, fmt.Sprintf("%s(%s)", v.Name, string(v.Args)))
		}
	}
	return strings.Join(parts, "; ")
}

// clip truncates s at max characters, appending the truncation marker Pi
// uses so summaries acknowledge missing tail content.
func clip(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + fmt.Sprintf(" [... %d more characters truncated]", len(s)-max)
}
