package faux

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/user/mixi-agent/internal/ai"
)

const sampleScript = `{
  "turns": [
    {"text": "writing now", "toolCalls": [{"id": "t1", "name": "write", "args": {"path": "hello.txt", "content": "hi"}}]},
    {"text": "done"}
  ]
}`

// newLoaded builds a provider with the script already parsed, bypassing the
// process-wide once so each test controls its own script.
func newLoaded(t *testing.T, script string) *provider {
	t.Helper()
	path := filepath.Join(t.TempDir(), "script.json")
	if err := os.WriteFile(path, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(ScriptEnv, path)
	p := &provider{}
	p.once.Do(p.load)
	if p.err != nil {
		t.Fatalf("load: %v", p.err)
	}
	return p
}

func drain(t *testing.T, ch <-chan ai.StreamEvent) ai.AssistantMessage {
	t.Helper()
	var final ai.AssistantMessage
	got := false
	for ev := range ch {
		switch e := ev.(type) {
		case ai.EventDone:
			final, got = e.Message, true
		case ai.EventError:
			final, got = e.Message, true
		}
	}
	if !got {
		t.Fatal("stream closed without terminal event")
	}
	return final
}

func TestScriptedTurnsReplayInOrder(t *testing.T) {
	p := newLoaded(t, sampleScript)
	m := Model()

	first := drain(t, p.Stream(context.Background(), m, ai.Context{}, ai.StreamOptions{}))
	if first.StopReason != ai.StopReasonToolUse {
		t.Fatalf("turn 1 stopReason = %s, want toolUse", first.StopReason)
	}
	var call *ai.ToolCall
	for _, c := range first.Content {
		if tc, ok := c.(ai.ToolCall); ok {
			call = &tc
		}
	}
	if call == nil || call.Name != "write" || call.ID != "t1" {
		t.Fatalf("turn 1 tool call = %+v", call)
	}

	second := drain(t, p.Stream(context.Background(), m, ai.Context{}, ai.StreamOptions{}))
	if second.StopReason != ai.StopReasonStop {
		t.Fatalf("turn 2 stopReason = %s, want stop", second.StopReason)
	}

	// Beyond the script: error stream, not a hang.
	third := drain(t, p.Stream(context.Background(), m, ai.Context{}, ai.StreamOptions{}))
	if third.StopReason != ai.StopReasonError {
		t.Fatalf("turn 3 stopReason = %s, want error", third.StopReason)
	}
}

func TestMissingScriptEnvYieldsErrorStream(t *testing.T) {
	t.Setenv(ScriptEnv, "")
	p := &provider{}
	final := drain(t, p.Stream(context.Background(), Model(), ai.Context{}, ai.StreamOptions{}))
	if final.StopReason != ai.StopReasonError {
		t.Fatalf("stopReason = %s, want error", final.StopReason)
	}
}

func TestMalformedScriptYieldsErrorStream(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(ScriptEnv, path)
	p := &provider{}
	final := drain(t, p.Stream(context.Background(), Model(), ai.Context{}, ai.StreamOptions{}))
	if final.StopReason != ai.StopReasonError {
		t.Fatalf("stopReason = %s, want error", final.StopReason)
	}
}

func TestRegisteredInGlobalRegistry(t *testing.T) {
	if _, err := ai.Resolve("faux"); err != nil {
		t.Fatalf("faux provider not registered: %v", err)
	}
}
