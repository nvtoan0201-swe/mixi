//go:build live

package anthropic

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/user/mixi-agent/internal/ai"
)

// TestLiveComplete smoke-tests against the real API. Run with:
//
//	ANTHROPIC_API_KEY=... go test -tags=live -run TestLiveComplete ./internal/ai/anthropic/
func TestLiveComplete(t *testing.T) {
	key := os.Getenv("ANTHROPIC_API_KEY")
	if key == "" {
		t.Skip("ANTHROPIC_API_KEY not set")
	}
	model, _ := ai.Lookup("anthropic", "claude-haiku-4-5")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	msg, err := ai.Complete(ctx, model, ai.Context{
		Messages: []ai.Message{ai.UserMessage{
			Content:   []ai.Content{ai.TextContent{Text: "Reply with exactly the word: pong"}},
			Timestamp: time.Now().UnixMilli(),
		}},
	}, ai.StreamOptions{APIKey: key, MaxTokens: 64})
	if err != nil {
		t.Fatal(err)
	}
	if msg.StopReason != ai.StopReasonStop {
		t.Errorf("stop reason = %s, want stop", msg.StopReason)
	}
	if len(msg.Content) == 0 {
		t.Fatal("no content returned")
	}
	text, ok := msg.Content[0].(ai.TextContent)
	if !ok || text.Text == "" {
		t.Fatalf("unexpected first block: %+v", msg.Content[0])
	}
	if msg.Usage.Input == 0 || msg.Usage.Output == 0 || msg.Usage.Total == 0 {
		t.Errorf("usage incomplete: %+v", msg.Usage)
	}
	t.Logf("live reply: %q usage: %+v", text.Text, msg.Usage)
}
