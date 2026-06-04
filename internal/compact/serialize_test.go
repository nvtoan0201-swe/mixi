package compact

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/user/mixi-agent/internal/ai"
	"github.com/user/mixi-agent/internal/session"
)

func msgEntry(id string, m ai.Message) *session.MessageEntry {
	e := &session.MessageEntry{Message: m}
	e.ID = id
	return e
}

// TestSerializeEntriesGolden locks the serialized conversation shape the
// summarizer prompt depends on.
func TestSerializeEntriesGolden(t *testing.T) {
	entries := []session.Entry{
		msgEntry("e1", ai.UserMessage{Content: []ai.Content{ai.TextContent{Text: "add a --version flag"}}}),
		msgEntry("e2", ai.AssistantMessage{Content: []ai.Content{
			ai.ThinkingContent{Thinking: "Need to find main."},
			ai.TextContent{Text: "Looking for the entry point."},
			ai.ToolCall{ID: "t1", Name: "grep", Args: json.RawMessage(`{"pattern":"func main"}`)},
			ai.ToolCall{ID: "t2", Name: "ls", Args: json.RawMessage(`{}`)},
		}}),
		msgEntry("e3", ai.ToolResultMessage{ToolCallID: "t1", Content: []ai.Content{ai.TextContent{Text: "cmd/mixi/main.go:14:func main() {"}}}),
		msgEntry("e4", ai.AssistantMessage{Content: []ai.Content{ai.TextContent{Text: "Added the flag."}}}),
	}

	got := serializeEntries(entries)
	want := strings.Join([]string{
		"[User]: add a --version flag",
		"",
		"[Assistant thinking]: Need to find main.",
		"",
		"[Assistant]: Looking for the entry point.",
		"",
		`[Assistant tool calls]: grep({"pattern":"func main"}); ls({})`,
		"",
		"[Tool result]: cmd/mixi/main.go:14:func main() {",
		"",
		"[Assistant]: Added the flag.",
	}, "\n")
	if got != want {
		t.Fatalf("serialized conversation mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestSerializeClipsToolResults(t *testing.T) {
	long := text(toolResultMaxChars + 500)
	got := serializeEntries([]session.Entry{
		msgEntry("e1", ai.ToolResultMessage{Content: []ai.Content{ai.TextContent{Text: long}}}),
	})
	if !strings.Contains(got, "[... 500 more characters truncated]") {
		t.Fatalf("missing truncation marker in %q", got[len(got)-80:])
	}
	if len(got) > toolResultMaxChars+100 {
		t.Fatalf("clipped result still %d chars", len(got))
	}
}

func TestSerializeSkipsNonConversation(t *testing.T) {
	mc := &session.ModelChangeEntry{Provider: "anthropic", ModelID: "x"}
	mc.ID = "e1"
	cu := &session.CustomEntry{CustomType: "workingset", Data: json.RawMessage(`{}`)}
	cu.ID = "e2"
	bs := &session.BranchSummaryEntry{Summary: "explored git sha output"}
	bs.ID = "e3"

	got := serializeEntries([]session.Entry{mc, cu, bs})
	if strings.Contains(got, "anthropic") || strings.Contains(got, "workingset") {
		t.Fatalf("non-conversation entries leaked: %q", got)
	}
	if !strings.Contains(got, "[Branch summary]: explored git sha output") {
		t.Fatalf("branch summary missing: %q", got)
	}
}
