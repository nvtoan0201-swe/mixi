package ai

import "testing"

func TestLookupKnownModels(t *testing.T) {
	for _, id := range []string{"claude-opus-4-8", "claude-sonnet-4-6", "claude-haiku-4-5"} {
		m, ok := Lookup("anthropic", id)
		if !ok {
			t.Errorf("Lookup(anthropic, %s) not found", id)
			continue
		}
		if m.API != "anthropic-messages" {
			t.Errorf("%s API = %q, want anthropic-messages", id, m.API)
		}
		if m.ContextWindow <= 0 || m.MaxOutput <= 0 {
			t.Errorf("%s has unset limits: %+v", id, m)
		}
		if m.Pricing.Input <= 0 || m.Pricing.Output <= 0 {
			t.Errorf("%s has unset pricing: %+v", id, m.Pricing)
		}
		if !m.Caps.Thinking || !m.Caps.PromptCaching {
			t.Errorf("%s missing expected caps: %+v", id, m.Caps)
		}
	}
}

func TestLookupMiss(t *testing.T) {
	if _, ok := Lookup("anthropic", "no-such-model"); ok {
		t.Error("Lookup(no-such-model) = ok, want miss")
	}
	if _, ok := Lookup("nobody", "claude-sonnet-4-6"); ok {
		t.Error("Lookup(wrong provider) = ok, want miss")
	}
}

func TestModelsReturnsCopy(t *testing.T) {
	a := Models()
	a[0].ID = "mutated"
	b := Models()
	if b[0].ID == "mutated" {
		t.Error("Models() exposes internal catalog slice")
	}
}
