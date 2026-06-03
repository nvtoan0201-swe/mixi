package ai

import (
	"context"
	"fmt"
	"sync"
)

// Provider is the interface implemented by all AI provider packages.
type Provider interface {
	API() string // "anthropic-messages", "openai-chat"
	Stream(ctx context.Context, model Model, c Context, opts StreamOptions) <-chan AssistantMessageEvent
}

var (
	registryMu sync.RWMutex
	registry   = map[string]Provider{}
)

// Register adds a provider to the global registry. Providers call this from their init().
func Register(p Provider) {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry[p.API()] = p
}

// Resolve returns the provider registered for the given API string.
func Resolve(api string) (Provider, error) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	p, ok := registry[api]
	if !ok {
		return nil, fmt.Errorf("ai: no provider registered for API %q", api)
	}
	return p, nil
}
