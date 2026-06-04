package anthropic

import (
	"encoding/json"
	"fmt"

	"github.com/user/mixi-agent/internal/ai"
)

type wireMessage struct {
	Role    string      `json:"role"`
	Content []wireBlock `json:"content"`
}

// wireBlock is the superset of Anthropic content-block shapes; the Type
// field selects which subset of fields is populated.
type wireBlock struct {
	Type string `json:"type"`
	// text
	Text string `json:"text,omitempty"`
	// image
	Source *imageSource `json:"source,omitempty"`
	// thinking / redacted_thinking
	Thinking  string `json:"thinking,omitempty"`
	Signature string `json:"signature,omitempty"`
	Data      string `json:"data,omitempty"`
	// tool_use
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
	// tool_result
	ToolUseID string      `json:"tool_use_id,omitempty"`
	Content   []wireBlock `json:"content,omitempty"`
	IsError   bool        `json:"is_error,omitempty"`

	CacheControl *cacheControl `json:"cache_control,omitempty"`
}

type imageSource struct {
	Type      string `json:"type"` // always "base64"
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

// convertMessages maps the normalized history onto Anthropic wire messages.
// Consecutive tool results merge into one user message, and every tool_use
// is guaranteed a paired tool_result (synthetic empty one if the history
// lacks it — the API rejects unpaired tool_use blocks).
func convertMessages(msgs []ai.Message) ([]wireMessage, error) {
	var out []wireMessage
	for i := 0; i < len(msgs); {
		switch m := msgs[i].(type) {
		case ai.UserMessage:
			blocks, err := convertUserContent(m.Content)
			if err != nil {
				return nil, err
			}
			if len(blocks) > 0 {
				out = append(out, wireMessage{Role: "user", Content: blocks})
			}
			i++
		case ai.AssistantMessage:
			blocks, toolIDs := convertAssistantContent(m.Content)
			if len(blocks) == 0 { // e.g. fully aborted turn — nothing to replay
				i++
				continue
			}
			out = append(out, wireMessage{Role: "assistant", Content: blocks})

			// Consume the immediately-following tool results into one user turn.
			var results []wireBlock
			answered := make(map[string]bool)
			j := i + 1
			for ; j < len(msgs); j++ {
				tr, ok := msgs[j].(ai.ToolResultMessage)
				if !ok {
					break
				}
				blk, err := convertToolResult(tr)
				if err != nil {
					return nil, err
				}
				results = append(results, blk)
				answered[tr.ToolCallID] = true
			}
			for _, id := range toolIDs {
				if !answered[id] {
					results = append(results, wireBlock{Type: "tool_result", ToolUseID: id})
				}
			}
			if len(results) > 0 {
				out = append(out, wireMessage{Role: "user", Content: results})
			}
			i = j
		case ai.ToolResultMessage:
			// Result with no preceding assistant tool_use — the API would
			// reject it; drop rather than fail the whole request.
			i++
		default:
			return nil, fmt.Errorf("anthropic: unknown message type %T", m)
		}
	}
	return out, nil
}

func convertUserContent(content []ai.Content) ([]wireBlock, error) {
	var out []wireBlock
	for _, c := range content {
		switch v := c.(type) {
		case ai.TextContent:
			if v.Text != "" {
				out = append(out, wireBlock{Type: "text", Text: v.Text})
			}
		case ai.ImageContent:
			out = append(out, wireBlock{Type: "image", Source: &imageSource{Type: "base64", MediaType: v.MimeType, Data: v.Data}})
		default:
			return nil, fmt.Errorf("anthropic: unsupported user content %T", c)
		}
	}
	return out, nil
}

// convertAssistantContent replays an assistant turn; returns the blocks and
// the tool_use IDs in order (for result pairing).
func convertAssistantContent(content []ai.Content) (blocks []wireBlock, toolIDs []string) {
	for _, c := range content {
		switch v := c.(type) {
		case ai.TextContent:
			if v.Text != "" {
				blocks = append(blocks, wireBlock{Type: "text", Text: v.Text})
			}
		case ai.ThinkingContent:
			switch {
			case v.Redacted:
				blocks = append(blocks, wireBlock{Type: "redacted_thinking", Data: v.Signature})
			case v.Signature != "":
				blocks = append(blocks, wireBlock{Type: "thinking", Thinking: v.Thinking, Signature: v.Signature})
			case v.Thinking != "":
				// Signature-less thinking cannot be replayed as thinking
				// (API verifies signatures) — downgrade to plain text.
				blocks = append(blocks, wireBlock{Type: "text", Text: v.Thinking})
			}
		case ai.ToolCall:
			args := v.Args
			if len(args) == 0 {
				args = json.RawMessage("{}")
			}
			blocks = append(blocks, wireBlock{Type: "tool_use", ID: v.ID, Name: v.Name, Input: args})
			toolIDs = append(toolIDs, v.ID)
		}
	}
	return blocks, toolIDs
}

func convertToolResult(m ai.ToolResultMessage) (wireBlock, error) {
	inner, err := convertUserContent(m.Content)
	if err != nil {
		return wireBlock{}, err
	}
	return wireBlock{Type: "tool_result", ToolUseID: m.ToolCallID, Content: inner, IsError: m.IsError}, nil
}
