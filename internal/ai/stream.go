package ai

import "context"

// Stream resolves the provider for model.API and delegates to its Stream method.
func Stream(ctx context.Context, model Model, c Context, opts StreamOptions) (<-chan AssistantMessageEvent, error) {
	p, err := Resolve(model.API)
	if err != nil {
		return nil, err
	}
	return p.Stream(ctx, model, c, opts), nil
}
