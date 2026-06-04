// Package compact implements context compaction: usage-anchored token
// estimation, cut-point selection over the session path, conversation
// serialization for the summarizer, and the Compactor orchestration that
// appends a compaction entry only when summarization succeeds.
package compact

import (
	"github.com/user/mixi-agent/internal/agent"
	"github.com/user/mixi-agent/internal/ai"
	"github.com/user/mixi-agent/internal/session"
)

// Defaults tuned against the chars/4 heuristic; the reserve absorbs its
// drift on code-heavy content, so don't shrink one without retuning the
// other.
const (
	// DefaultReserveTokens is held back from the context window as headroom
	// for estimation error and the summary itself.
	DefaultReserveTokens = 16384
	// DefaultKeepRecentTokens is the tail of recent conversation a
	// compaction preserves verbatim.
	DefaultKeepRecentTokens = 20000
)

// estimateChars is the chars→tokens heuristic: ceil(chars/4).
func estimateChars(n int) int { return (n + 3) / 4 }

// EstimateMessage approximates one message's token cost from its visible
// characters: text+thinking+marshaled tool args for assistants, content for
// user/tool-result messages.
func EstimateMessage(m ai.Message) int {
	chars := 0
	switch v := m.(type) {
	case ai.UserMessage:
		chars = contentChars(v.Content)
	case ai.AssistantMessage:
		chars = contentChars(v.Content)
	case ai.ToolResultMessage:
		chars = contentChars(v.Content)
	}
	return estimateChars(chars)
}

func contentChars(cs []ai.Content) int {
	n := 0
	for _, c := range cs {
		switch v := c.(type) {
		case ai.TextContent:
			n += len(v.Text)
		case ai.ThinkingContent:
			n += len(v.Thinking)
		case ai.ToolCall:
			n += len(v.Name) + len(v.Args)
		case ai.ImageContent:
			n += len(v.Data)
		}
	}
	return n
}

// EstimateAgentMessage approximates one agent-level item's token cost.
// Harness items count the text their ToLLM projection would send.
func EstimateAgentMessage(m agent.AgentMessage) int {
	switch v := m.(type) {
	case agent.ModelMessage:
		if v.Excluded {
			return 0
		}
		return EstimateMessage(v.Msg)
	case agent.BashExecution:
		return estimateChars(len(v.Command) + len(v.Output))
	case agent.CompactionSummary:
		return estimateChars(len(v.Summary))
	case agent.BranchSummary:
		return estimateChars(len(v.Summary))
	case agent.CustomMessage:
		return estimateChars(len(v.Content))
	}
	return 0
}

// EstimateContext approximates the token footprint of a message list as the
// provider would see it: the last assistant message's reported usage total
// (ground truth, includes system prompt and tool definitions) plus estimates
// for everything after it. Without a usage anchor the whole list is
// estimated.
func EstimateContext(msgs []agent.AgentMessage) int {
	anchor := -1
	anchorTotal := 0
	for i := len(msgs) - 1; i >= 0; i-- {
		mm, ok := msgs[i].(agent.ModelMessage)
		if !ok {
			continue
		}
		am, ok := mm.Msg.(ai.AssistantMessage)
		if !ok || am.Usage.Total == 0 {
			continue
		}
		anchor, anchorTotal = i, am.Usage.Total
		break
	}
	total := anchorTotal
	for i := anchor + 1; i < len(msgs); i++ {
		total += EstimateAgentMessage(msgs[i])
	}
	return total
}

// EstimateContextPure is the anchor-free estimate: the plain sum of
// per-message heuristics. Used right after a compaction, when every recorded
// usage total still describes the pre-compaction context and would mask the
// shrink.
func EstimateContextPure(msgs []agent.AgentMessage) int {
	total := 0
	for _, m := range msgs {
		total += EstimateAgentMessage(m)
	}
	return total
}

// estimateEntry approximates a session entry's token cost for the cut-point
// walk. Entries that never reach the LLM cost nothing.
func estimateEntry(e session.Entry) int {
	switch v := e.(type) {
	case *session.MessageEntry:
		return EstimateMessage(v.Message)
	case *session.CustomMessageEntry:
		return estimateChars(len(v.Content))
	case *session.BranchSummaryEntry:
		return estimateChars(len(v.Summary))
	case *session.CompactionEntry:
		return estimateChars(len(v.Summary))
	}
	return 0
}
