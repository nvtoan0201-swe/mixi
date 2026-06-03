package ai

import (
	"context"
	"fmt"
	"sync"
)

// Provider streams completions for one wire API. Implementations register
// themselves at init() time via Register.
type Provider interface {
	// API returns the registry key, e.g. "anthropic-messages".
	API() string
	// Stream MUST: return a channel that emits the StreamEvent order contract
	// (see events.go) and is closed after Done/Error; honor ctx cancellation
	// by emitting EventError{Reason: StopReasonAborted}; never panic.
	Stream(ctx context.Context, model Model, c Context, opts StreamOptions) <-chan StreamEvent
}

var registry = struct {
	sync.RWMutex
	providers map[string]Provider
}{providers: make(map[string]Provider)}

// Register adds a provider under its API key. Intended for init()-time
// self-registration; panics on duplicate keys to surface wiring bugs early.
func Register(p Provider) {
	registry.Lock()
	defer registry.Unlock()
	key := p.API()
	if _, dup := registry.providers[key]; dup {
		panic(fmt.Sprintf("ai: provider already registered for API %q", key))
	}
	registry.providers[key] = p
}

// Resolve returns the provider registered for the given API key.
func Resolve(api string) (Provider, error) {
	registry.RLock()
	defer registry.RUnlock()
	p, ok := registry.providers[api]
	if !ok {
		return nil, fmt.Errorf("ai: no provider registered for API %q", api)
	}
	return p, nil
}
