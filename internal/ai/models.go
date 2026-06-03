package ai

// Built-in model catalog. Pricing is USD per million tokens, sourced from
// Anthropic's published rates; context/output limits from the models docs.
var builtinModels = []Model{
	{
		API:           "anthropic-messages",
		Provider:      "anthropic",
		ID:            "claude-opus-4-8",
		DisplayName:   "Claude Opus 4.8",
		ContextWindow: 200_000,
		MaxOutput:     64_000,
		Caps: Caps{
			Vision:        true,
			Thinking:      true,
			PromptCaching: true,
			LongCacheTTL:  true,
			ParallelTools: true,
		},
		Pricing: Price{Input: 5, Output: 25, CacheRead: 0.50, CacheWrite: 6.25},
	},
	{
		API:           "anthropic-messages",
		Provider:      "anthropic",
		ID:            "claude-sonnet-4-6",
		DisplayName:   "Claude Sonnet 4.6",
		ContextWindow: 200_000,
		MaxOutput:     64_000,
		Caps: Caps{
			Vision:        true,
			Thinking:      true,
			PromptCaching: true,
			LongCacheTTL:  true,
			ParallelTools: true,
		},
		Pricing: Price{Input: 3, Output: 15, CacheRead: 0.30, CacheWrite: 3.75},
	},
	{
		API:           "anthropic-messages",
		Provider:      "anthropic",
		ID:            "claude-haiku-4-5",
		DisplayName:   "Claude Haiku 4.5",
		ContextWindow: 200_000,
		MaxOutput:     64_000,
		Caps: Caps{
			Vision:        true,
			Thinking:      true,
			PromptCaching: true,
			LongCacheTTL:  true,
			ParallelTools: true,
		},
		Pricing: Price{Input: 1, Output: 5, CacheRead: 0.10, CacheWrite: 1.25},
	},
}

// Lookup returns the built-in catalog entry for (provider, id).
func Lookup(provider, id string) (Model, bool) {
	for _, m := range builtinModels {
		if m.Provider == provider && m.ID == id {
			return m, true
		}
	}
	return Model{}, false
}

// Models returns a copy of the built-in model catalog.
func Models() []Model {
	out := make([]Model, len(builtinModels))
	copy(out, builtinModels)
	return out
}
