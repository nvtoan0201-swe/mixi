package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/user/mixi-agent/internal/ai"
)

const (
	apiURL        = "https://api.anthropic.com/v1/messages"
	apiVersion    = "2023-06-01"
	defaultMaxOut = 4096
)

// ── Anthropic request types ───────────────────────────────────────────────────

type apiRequest struct {
	Model       string       `json:"model"`
	MaxTokens   int          `json:"max_tokens"`
	Stream      bool         `json:"stream"`
	System      []apiBlock   `json:"system,omitempty"`
	Messages    []apiMessage `json:"messages"`
	Tools       []apiTool    `json:"tools,omitempty"`
	Thinking    *apiThinking `json:"thinking,omitempty"`
	Temperature *float64     `json:"temperature,omitempty"`
}

type apiMessage struct {
	Role    string     `json:"role"`
	Content []apiBlock `json:"content"`
}

type apiBlock struct {
	Type         string          `json:"type"`
	Text         string          `json:"text,omitempty"`
	Thinking     string          `json:"thinking,omitempty"`
	Signature    string          `json:"signature,omitempty"`
	ID           string          `json:"id,omitempty"`
	Name         string          `json:"name,omitempty"`
	Input        json.RawMessage `json:"input,omitempty"`
	Source       *apiImageSource `json:"source,omitempty"`
	ToolUseID    string          `json:"tool_use_id,omitempty"`
	Content      []apiBlock      `json:"content,omitempty"` // nested in tool_result
	IsError      bool            `json:"is_error,omitempty"`
	CacheControl *apiCache       `json:"cache_control,omitempty"`
}

type apiImageSource struct {
	Type      string `json:"type"`       // "base64"
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

type apiTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type apiThinking struct {
	Type         string `json:"type"`          // "enabled"
	BudgetTokens int    `json:"budget_tokens"`
}

type apiCache struct {
	Type string `json:"type"` // "ephemeral"
}

// ── Request building ──────────────────────────────────────────────────────────

func buildHTTPRequest(ctx context.Context, apiKey string, req *apiRequest) (*http.Request, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("anthropic: marshal request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("anthropic: create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", apiKey)
	httpReq.Header.Set("anthropic-version", apiVersion)
	httpReq.Header.Set("Accept", "text/event-stream")
	return httpReq, nil
}

func buildAPIRequest(model ai.Model, c ai.Context, opts ai.StreamOptions) (*apiRequest, error) {
	maxTokens := defaultMaxOut
	if opts.MaxTokens != nil {
		maxTokens = *opts.MaxTokens
	} else if model.MaxOutput > 0 {
		maxTokens = model.MaxOutput
	}

	req := &apiRequest{
		Model:     model.Name,
		MaxTokens: maxTokens,
		Stream:    true,
	}

	// Temperature is not compatible with extended thinking
	if opts.Temperature != nil && model.Thinking == ai.ThinkingOff {
		req.Temperature = opts.Temperature
	}

	if model.Thinking != ai.ThinkingOff {
		req.Thinking = &apiThinking{Type: "enabled", BudgetTokens: thinkingBudget(model.Thinking)}
	}

	if c.SystemPrompt != "" {
		req.System = []apiBlock{{Type: "text", Text: c.SystemPrompt}}
	}

	for _, t := range c.Tools {
		req.Tools = append(req.Tools, apiTool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.Parameters,
		})
	}

	msgs, err := convertMessages(c.Messages)
	if err != nil {
		return nil, err
	}
	req.Messages = msgs

	injectCacheControl(req, opts.CacheBreakpoints)
	return req, nil
}

func thinkingBudget(level ai.ThinkingLevel) int {
	switch level {
	case ai.ThinkingLow:
		return 2048
	case ai.ThinkingHigh:
		return 32768
	default: // ThinkingMedium
		return 8192
	}
}

// convertMessages normalizes ai.Message slice to Anthropic messages,
// batching consecutive ToolResultMessages into a single user message.
func convertMessages(msgs []ai.Message) ([]apiMessage, error) {
	var result []apiMessage
	i := 0
	for i < len(msgs) {
		if _, ok := msgs[i].(ai.ToolResultMessage); ok {
			var blocks []apiBlock
			for i < len(msgs) {
				tr, ok := msgs[i].(ai.ToolResultMessage)
				if !ok {
					break
				}
				b, err := toolResultBlock(tr)
				if err != nil {
					return nil, err
				}
				blocks = append(blocks, b)
				i++
			}
			result = append(result, apiMessage{Role: "user", Content: blocks})
			continue
		}

		switch m := msgs[i].(type) {
		case ai.UserMessage:
			am, err := userMsgToAPI(m)
			if err != nil {
				return nil, err
			}
			result = append(result, am)
		case ai.AssistantMessage:
			am, err := assistantMsgToAPI(m)
			if err != nil {
				return nil, err
			}
			result = append(result, am)
		default:
			return nil, fmt.Errorf("anthropic: unknown message type %T", msgs[i])
		}
		i++
	}
	return result, nil
}

func userMsgToAPI(m ai.UserMessage) (apiMessage, error) {
	blocks, err := convertUserBlocks(m.Content)
	if err != nil {
		return apiMessage{}, err
	}
	return apiMessage{Role: "user", Content: blocks}, nil
}

func assistantMsgToAPI(m ai.AssistantMessage) (apiMessage, error) {
	var blocks []apiBlock
	for _, cb := range m.Content {
		b, err := assistantBlockToAPI(cb)
		if err != nil {
			return apiMessage{}, err
		}
		blocks = append(blocks, b)
	}
	return apiMessage{Role: "assistant", Content: blocks}, nil
}

func assistantBlockToAPI(cb ai.ContentBlock) (apiBlock, error) {
	switch c := cb.(type) {
	case ai.TextContent:
		return apiBlock{Type: "text", Text: c.Text}, nil
	case ai.ThinkingContent:
		return apiBlock{Type: "thinking", Thinking: c.Thinking, Signature: c.Signature}, nil
	case ai.ToolCall:
		input := c.Args
		if len(input) == 0 {
			input = json.RawMessage("{}")
		}
		return apiBlock{Type: "tool_use", ID: c.ID, Name: c.Name, Input: input}, nil
	default:
		return apiBlock{}, fmt.Errorf("anthropic: unknown assistant content block %T", cb)
	}
}

func convertUserBlocks(blocks []ai.ContentBlock) ([]apiBlock, error) {
	var result []apiBlock
	for _, b := range blocks {
		switch c := b.(type) {
		case ai.TextContent:
			result = append(result, apiBlock{Type: "text", Text: c.Text})
		case ai.ImageContent:
			result = append(result, apiBlock{
				Type:   "image",
				Source: &apiImageSource{Type: "base64", MediaType: c.MediaType, Data: c.Data},
			})
		default:
			return nil, fmt.Errorf("anthropic: unknown user content block %T", b)
		}
	}
	return result, nil
}

func toolResultBlock(tr ai.ToolResultMessage) (apiBlock, error) {
	content, err := convertUserBlocks(tr.Content)
	if err != nil {
		return apiBlock{}, err
	}
	return apiBlock{
		Type:      "tool_result",
		ToolUseID: tr.ToolCallID,
		Content:   content,
		IsError:   tr.IsError,
	}, nil
}

// injectCacheControl injects cache_control into the last block at each breakpoint.
// Breakpoint 0 = system prompt; breakpoint N (N≥1) = messages[N-1].
func injectCacheControl(req *apiRequest, breakpoints []int) {
	cache := &apiCache{Type: "ephemeral"}
	for _, bp := range breakpoints {
		if bp == 0 {
			if n := len(req.System); n > 0 {
				req.System[n-1].CacheControl = cache
			}
		} else if idx := bp - 1; idx < len(req.Messages) {
			if n := len(req.Messages[idx].Content); n > 0 {
				req.Messages[idx].Content[n-1].CacheControl = cache
			}
		}
	}
}
