package anthropic

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/user/mixi-agent/internal/ai"
)

func cachingModel() ai.Model {
	m, _ := ai.Lookup("anthropic", "claude-sonnet-4-6")
	return m
}

func sampleContext() ai.Context {
	return ai.Context{
		SystemPrompt: "You are helpful.",
		Tools: []ai.ToolDef{
			{Name: "grep", Description: "search", Schema: json.RawMessage(`{"type":"object"}`)},
			{Name: "read", Description: "read file", Schema: json.RawMessage(`{"type":"object"}`)},
		},
		Messages: []ai.Message{
			ai.UserMessage{Content: []ai.Content{ai.TextContent{Text: "find main"}}},
			ai.AssistantMessage{Content: []ai.Content{
				ai.ToolCall{ID: "toolu_1", Name: "grep", Args: json.RawMessage(`{"pattern":"main"}`)},
			}},
			ai.ToolResultMessage{ToolCallID: "toolu_1", ToolName: "grep",
				Content: []ai.Content{ai.TextContent{Text: "main.go:1"}}},
			ai.UserMessage{Content: []ai.Content{
				ai.TextContent{Text: "first"},
				ai.TextContent{Text: "now read it"},
			}},
		},
	}
}

// decodeBody unmarshals the built request into a generic map for structural
// assertions.
func decodeBody(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func cacheControlOf(v any) map[string]any {
	blk, _ := v.(map[string]any)
	cc, _ := blk["cache_control"].(map[string]any)
	return cc
}

func TestCacheControlThreePoints(t *testing.T) {
	body, _, err := buildRequest(cachingModel(), sampleContext(), ai.StreamOptions{CacheRetention: ai.CacheShort})
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(body), `"cache_control"`); n != 3 {
		t.Fatalf("cache_control occurrences = %d, want exactly 3\nbody: %s", n, body)
	}
	req := decodeBody(t, body)

	system := req["system"].([]any)
	if cc := cacheControlOf(system[len(system)-1]); cc["type"] != "ephemeral" {
		t.Error("last system block missing ephemeral cache_control")
	}
	tools := req["tools"].([]any)
	if cc := cacheControlOf(tools[0]); cc != nil {
		t.Error("first tool must not carry cache_control")
	}
	if cc := cacheControlOf(tools[len(tools)-1]); cc["type"] != "ephemeral" {
		t.Error("last tool missing ephemeral cache_control")
	}

	msgs := req["messages"].([]any)
	last := msgs[len(msgs)-1].(map[string]any)
	if last["role"] != "user" {
		t.Fatalf("last wire message role = %v, want user", last["role"])
	}
	blocks := last["content"].([]any)
	if cc := cacheControlOf(blocks[len(blocks)-1]); cc["type"] != "ephemeral" {
		t.Error("last block of last user message missing cache_control")
	}
	if cc := cacheControlOf(blocks[0]); cc != nil {
		t.Error("non-final block of last user message must not carry cache_control")
	}
}

func TestCacheControlLongTTL(t *testing.T) {
	body, _, err := buildRequest(cachingModel(), sampleContext(), ai.StreamOptions{CacheRetention: ai.CacheLong})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"ttl":"1h"`) {
		t.Error("long retention should set ttl 1h on a long-TTL-capable model")
	}
}

func TestCacheControlDisabled(t *testing.T) {
	for name, tc := range map[string]struct {
		model ai.Model
		opts  ai.StreamOptions
	}{
		"retention none": {cachingModel(), ai.StreamOptions{CacheRetention: ai.CacheNone}},
		"model without caching": {func() ai.Model {
			m := cachingModel()
			m.Caps.PromptCaching = false
			return m
		}(), ai.StreamOptions{CacheRetention: ai.CacheShort}},
	} {
		body, _, err := buildRequest(tc.model, sampleContext(), tc.opts)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(body), "cache_control") {
			t.Errorf("%s: cache_control must be absent", name)
		}
	}
}

func TestThinkingBudgetsAndCarveIn(t *testing.T) {
	model := cachingModel() // MaxOutput 64000
	for level, wantBudget := range map[ai.ThinkingLevel]int{
		ai.ThinkingLow:    2048,
		ai.ThinkingMedium: 8192,
		ai.ThinkingHigh:   16384,
	} {
		body, _, err := buildRequest(model, sampleContext(), ai.StreamOptions{Thinking: level, MaxTokens: 4000})
		if err != nil {
			t.Fatal(err)
		}
		req := decodeBody(t, body)
		think := req["thinking"].(map[string]any)
		if think["type"] != "enabled" || int(think["budget_tokens"].(float64)) != wantBudget {
			t.Errorf("%s: thinking = %v, want enabled/%d", level, think, wantBudget)
		}
		// Budget carved into max_tokens: min(base+budget, model max).
		if got := int(req["max_tokens"].(float64)); got != 4000+wantBudget {
			t.Errorf("%s: max_tokens = %d, want %d", level, got, 4000+wantBudget)
		}
	}

	// Explicit budget overrides the level default; carve-in clamps to model max.
	body, _, err := buildRequest(model, sampleContext(), ai.StreamOptions{
		Thinking: ai.ThinkingHigh, ThinkingBudget: 60000,
	})
	if err != nil {
		t.Fatal(err)
	}
	req := decodeBody(t, body)
	if got := int(req["thinking"].(map[string]any)["budget_tokens"].(float64)); got != 60000 {
		t.Errorf("budget override = %d, want 60000", got)
	}
	if got := int(req["max_tokens"].(float64)); got != model.MaxOutput {
		t.Errorf("max_tokens = %d, want clamp to model max %d", got, model.MaxOutput)
	}
}

func TestTemperatureDroppedWhenThinking(t *testing.T) {
	temp := 0.5
	body, _, err := buildRequest(cachingModel(), sampleContext(), ai.StreamOptions{Temperature: &temp, Thinking: ai.ThinkingLow})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), `"temperature"`) {
		t.Error("temperature must be omitted when thinking is enabled")
	}
	body, _, err = buildRequest(cachingModel(), sampleContext(), ai.StreamOptions{Temperature: &temp})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"temperature":0.5`) {
		t.Error("temperature missing when thinking is off")
	}
}

func TestBetaHeaders(t *testing.T) {
	_, betas, err := buildRequest(cachingModel(), sampleContext(), ai.StreamOptions{Thinking: ai.ThinkingMedium})
	if err != nil {
		t.Fatal(err)
	}
	want := betaFineGrainedToolStreaming + "," + betaInterleavedThinking
	if betas != want {
		t.Errorf("betas = %q, want %q", betas, want)
	}

	noTools := sampleContext()
	noTools.Tools = nil
	_, betas, err = buildRequest(cachingModel(), noTools, ai.StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if betas != "" {
		t.Errorf("betas = %q, want empty without tools/thinking", betas)
	}
}

func TestMaxTokensDefaultsToModelMax(t *testing.T) {
	model := cachingModel()
	body, _, err := buildRequest(model, sampleContext(), ai.StreamOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := int(decodeBody(t, body)["max_tokens"].(float64)); got != model.MaxOutput {
		t.Errorf("max_tokens = %d, want model max %d", got, model.MaxOutput)
	}
}
