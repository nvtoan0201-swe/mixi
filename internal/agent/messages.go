// Package agent implements the two-level agent runtime loop: streaming
// turns, tool dispatch, steering/follow-up queues, retries, and event
// fan-out. It is fully testable against a scripted provider (agenttest).
package agent

import (
	"fmt"

	"github.com/user/mixi-agent/internal/ai"
)

// AgentMessage is the sealed union of conversation items the agent tracks.
// It is a superset of ai.Message: model-visible messages plus harness-level
// items (shell executions, compaction/branch summaries, custom notes) that
// project into LLM-visible user messages — or nothing — via ToLLM.
type AgentMessage interface{ isAgentMessage() }

// ModelMessage wraps a provider-level message (user/assistant/toolResult).
type ModelMessage struct {
	Msg ai.Message
	// Excluded drops a tool-result message from LLM context while keeping
	// it in the session (e.g. superseded reads after compaction).
	Excluded bool
}

// BashExecution records a user-run shell command (TUI `!cmd`).
type BashExecution struct {
	Command   string
	Output    string
	ExitCode  int
	Timestamp int64
}

// CompactionSummary replaces compacted history.
type CompactionSummary struct {
	Summary   string
	Timestamp int64
}

// BranchSummary carries context from an abandoned session branch.
type BranchSummary struct {
	Summary   string
	Timestamp int64
}

// CustomMessage is extension-injected content delivered as a user message.
type CustomMessage struct {
	Content   string
	Timestamp int64
}

func (ModelMessage) isAgentMessage()      {}
func (BashExecution) isAgentMessage()     {}
func (CompactionSummary) isAgentMessage() {}
func (BranchSummary) isAgentMessage()     {}
func (CustomMessage) isAgentMessage()     {}

// ToLLM projects agent messages into the provider-facing message list:
// summaries become <summary>-wrapped user messages, bash executions become
// formatted user messages, excluded tool results are dropped.
func ToLLM(msgs []AgentMessage) []ai.Message {
	out := make([]ai.Message, 0, len(msgs))
	for _, m := range msgs {
		switch v := m.(type) {
		case ModelMessage:
			if v.Excluded {
				continue
			}
			// A content-less assistant message (failed turn recorded for
			// persistence) is not representable wire-side; providers reject
			// empty assistant turns, so it never reaches the LLM.
			if am, ok := v.Msg.(ai.AssistantMessage); ok && len(am.Content) == 0 {
				continue
			}
			out = append(out, v.Msg)
		case BashExecution:
			text := fmt.Sprintf("I ran this command:\n```\n%s\n```\nOutput (exit code %d):\n```\n%s\n```", v.Command, v.ExitCode, v.Output)
			out = append(out, userText(text, v.Timestamp))
		case CompactionSummary:
			out = append(out, userText("<summary>\n"+v.Summary+"\n</summary>", v.Timestamp))
		case BranchSummary:
			out = append(out, userText("<summary>\n"+v.Summary+"\n</summary>", v.Timestamp))
		case CustomMessage:
			out = append(out, userText(v.Content, v.Timestamp))
		}
	}
	return out
}

func userText(text string, ts int64) ai.UserMessage {
	return ai.UserMessage{Content: []ai.Content{ai.TextContent{Text: text}}, Timestamp: ts}
}
