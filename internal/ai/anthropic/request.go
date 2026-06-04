package anthropic

import (
	"encoding/json"
	"strings"

	"github.com/user/mixi-agent/internal/ai"
)

// Beta features enabled per request shape.
const (
	betaFineGrainedToolStreaming = "fine-grained-tool-streaming-2025-05-14"
	betaInterleavedThinking      = "interleaved-thinking-2025-05-14"
)

// Level-derived thinking budgets (tokens) when StreamOptions.ThinkingBudget
// is unset.
var thinkingBudgets = map[ai.ThinkingLevel]int{
	ai.ThinkingLow:    2048,
	ai.ThinkingMedium: 8192,
	ai.ThinkingHigh:   16384,
}

type cacheControl struct {
	Type string `json:"type"`
	TTL  string `json:"ttl,omitempty"`
}

type systemBlock struct {
	Type         string        `json:"type"`
	Text         string        `json:"text"`
	CacheControl *cacheControl `json:"cache_control,omitempty"`
}

type wireTool struct {
	Name         string          `json:"name"`
	Description  string          `json:"description"`
	InputSchema  json.RawMessage `json:"input_schema"`
	CacheControl *cacheControl   `json:"cache_control,omitempty"`
}

type thinkingConfig struct {
	Type         string `json:"type"`
	BudgetTokens int    `json:"budget_tokens"`
}

type request struct {
	Model       string          `json:"model"`
	MaxTokens   int             `json:"max_tokens"`
	System      []systemBlock   `json:"system,omitempty"`
	Messages    []wireMessage   `json:"messages"`
	Tools       []wireTool      `json:"tools,omitempty"`
	Temperature *float64        `json:"temperature,omitempty"`
	Thinking    *thinkingConfig `json:"thinking,omitempty"`
	Stream      bool            `json:"stream"`
}

// buildRequest constructs the /v1/messages JSON body and the anthropic-beta
// header value for the given model, context, and options.
func buildRequest(model ai.Model, c ai.Context, opts ai.StreamOptions) (body []byte, betas string, err error) {
	msgs, err := convertMessages(c.Messages)
	if err != nil {
		return nil, "", err
	}

	req := request{
		Model:    model.ID,
		Messages: msgs,
		Stream:   true,
	}

	cc := buildCacheControl(model, opts)

	if c.SystemPrompt != "" {
		req.System = []systemBlock{{Type: "text", Text: c.SystemPrompt, CacheControl: cc}}
	}

	for _, t := range c.Tools {
		schema := t.Schema
		if len(schema) == 0 {
			schema = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		req.Tools = append(req.Tools, wireTool{Name: t.Name, Description: t.Description, InputSchema: schema})
	}
	if n := len(req.Tools); n > 0 {
		req.Tools[n-1].CacheControl = cc
	}

	attachUserCachePoint(req.Messages, cc)

	base := opts.MaxTokens
	if base <= 0 || (model.MaxOutput > 0 && base > model.MaxOutput) {
		base = model.MaxOutput
	}
	req.MaxTokens = base

	var betaList []string
	if len(req.Tools) > 0 {
		betaList = append(betaList, betaFineGrainedToolStreaming)
	}

	if opts.Thinking != "" && opts.Thinking != ai.ThinkingOff && model.Caps.Thinking {
		budget := opts.ThinkingBudget
		if budget <= 0 {
			budget = thinkingBudgets[opts.Thinking]
		}
		req.Thinking = &thinkingConfig{Type: "enabled", BudgetTokens: budget}
		// Budget is carved inside max_tokens, not additive beyond model max.
		// A model with unknown MaxOutput (0) must not collapse max_tokens to 0.
		req.MaxTokens = base + budget
		if model.MaxOutput > 0 {
			req.MaxTokens = min(base+budget, model.MaxOutput)
		}
		betaList = append(betaList, betaInterleavedThinking)
	} else if opts.Temperature != nil {
		// The API rejects custom temperature when thinking is enabled.
		req.Temperature = opts.Temperature
	}

	body, err = json.Marshal(req)
	return body, strings.Join(betaList, ","), err
}

// buildCacheControl returns the per-breakpoint cache_control marker, or nil
// when caching is off or unsupported by the model.
func buildCacheControl(model ai.Model, opts ai.StreamOptions) *cacheControl {
	if !model.Caps.PromptCaching || opts.CacheRetention == ai.CacheNone {
		return nil
	}
	cc := &cacheControl{Type: "ephemeral"}
	if opts.CacheRetention == ai.CacheLong && model.Caps.LongCacheTTL {
		cc.TTL = "1h"
	}
	return cc
}

// attachUserCachePoint marks the last block of the last user-role wire
// message as the conversation cache breakpoint.
func attachUserCachePoint(msgs []wireMessage, cc *cacheControl) {
	if cc == nil {
		return
	}
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role != "user" || len(msgs[i].Content) == 0 {
			continue
		}
		msgs[i].Content[len(msgs[i].Content)-1].CacheControl = cc
		return
	}
}
