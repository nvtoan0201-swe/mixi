package ai

import (
	"context"
	"errors"
	"fmt"
)

// Stream resolves the provider for model.API and delegates the request.
// The returned channel follows the StreamEvent order contract (events.go).
func Stream(ctx context.Context, model Model, c Context, opts StreamOptions) (<-chan StreamEvent, error) {
	p, err := Resolve(model.API)
	if err != nil {
		return nil, err
	}
	return p.Stream(ctx, model, c, opts), nil
}

// Complete runs Stream and drains the channel to the final message.
// EventError yields the partial message together with a non-nil error.
func Complete(ctx context.Context, model Model, c Context, opts StreamOptions) (AssistantMessage, error) {
	ch, err := Stream(ctx, model, c, opts)
	if err != nil {
		return AssistantMessage{}, err
	}
	var final AssistantMessage
	var streamErr error
	terminated := false
	for ev := range ch {
		switch e := ev.(type) {
		case EventDone:
			final = e.Message
			terminated = true
		case EventError:
			final = e.Message
			msg := e.Message.ErrorMessage
			if msg == "" {
				msg = string(e.Reason)
			}
			streamErr = fmt.Errorf("ai: stream failed (%s): %s", e.Reason, msg)
			terminated = true
		}
	}
	if !terminated {
		return final, errors.New("ai: stream closed without Done or Error event")
	}
	return final, streamErr
}
