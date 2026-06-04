package compact

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/user/mixi-agent/internal/agent"
	"github.com/user/mixi-agent/internal/ai"
)

// Summarizer produces a summary from a serialized conversation. The real
// implementation calls an LLM; tests use a scripted fake.
type Summarizer interface {
	Summarize(ctx context.Context, conversation string, prompt string, maxTokens int) (string, error)
}

// LLMSummarizer summarizes through a provider stream — by default the same
// model the agent is running on.
type LLMSummarizer struct {
	Model  ai.Model
	APIKey string
	// Stream overrides the registry lookup (tests inject scripted streams).
	Stream agent.StreamFunc
}

func (s *LLMSummarizer) Summarize(ctx context.Context, conversation, prompt string, maxTokens int) (string, error) {
	c := ai.Context{Messages: []ai.Message{ai.UserMessage{
		Content:   []ai.Content{ai.TextContent{Text: conversation + "\n\n" + prompt}},
		Timestamp: time.Now().UnixMilli(),
	}}}
	opts := ai.StreamOptions{APIKey: s.APIKey, MaxTokens: maxTokens}

	stream := s.Stream
	if stream == nil {
		stream = func(ctx context.Context, model ai.Model, c ai.Context, opts ai.StreamOptions) <-chan ai.StreamEvent {
			ch, err := ai.Stream(ctx, model, c, opts)
			if err != nil {
				out := make(chan ai.StreamEvent, 1)
				out <- ai.EventError{Reason: ai.StopReasonError, Message: ai.AssistantMessage{
					StopReason: ai.StopReasonError, ErrorMessage: err.Error(),
				}}
				close(out)
				return out
			}
			return ch
		}
	}

	var final ai.AssistantMessage
	got := false
	for ev := range stream(ctx, s.Model, c, opts) {
		switch e := ev.(type) {
		case ai.EventDone:
			final, got = e.Message, true
		case ai.EventError:
			final, got = e.Message, true
		}
	}
	if !got {
		return "", fmt.Errorf("summarizer: stream closed without a terminal event")
	}
	if final.StopReason == ai.StopReasonError || final.StopReason == ai.StopReasonAborted {
		return "", fmt.Errorf("summarizer: %s", final.ErrorMessage)
	}
	text := assistantText(final)
	if strings.TrimSpace(text) == "" {
		return "", fmt.Errorf("summarizer: model returned no text")
	}
	return text, nil
}

func assistantText(m ai.AssistantMessage) string {
	var parts []string
	for _, c := range m.Content {
		if t, ok := c.(ai.TextContent); ok {
			parts = append(parts, t.Text)
		}
	}
	return strings.Join(parts, "\n")
}
