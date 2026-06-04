package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/user/mixi-agent/internal/agent/agenttest"
	"github.com/user/mixi-agent/internal/ai"
)

func newTestAgent(p *agenttest.Provider, opts ...func(*Config)) *Agent {
	cfg := Config{Model: testModel, Stream: p.Stream, Log: silentLog}
	for _, o := range opts {
		o(&cfg)
	}
	return New(cfg)
}

// waitEnd drains events until EvAgentEnd and returns its reason.
func waitEnd(t *testing.T, ch <-chan Event) EndReason {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				t.Fatal("event channel closed before EvAgentEnd")
			}
			if end, isEnd := ev.(EvAgentEnd); isEnd {
				return end.Reason
			}
		case <-deadline:
			t.Fatal("timed out waiting for EvAgentEnd")
		}
	}
}

func TestAgentPromptAccumulatesMessages(t *testing.T) {
	a := newTestAgent(agenttest.New(
		agenttest.Turn{Text: "first reply"},
		agenttest.Turn{Text: "second reply"},
	))
	if err := a.Prompt(context.Background(), "one"); err != nil {
		t.Fatal(err)
	}
	if err := a.Prompt(context.Background(), "two"); err != nil {
		t.Fatal(err)
	}
	if got := len(a.Messages()); got != 4 {
		t.Fatalf("messages = %d, want 4", got)
	}
}

func TestAgentErrBusyOnConcurrentPrompt(t *testing.T) {
	p := agenttest.New(agenttest.Turn{WaitCtx: true})
	a := newTestAgent(p)
	ch, cancel := a.Subscribe()
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- a.Prompt(context.Background(), "long run") }()

	// Wait for the run to actually start, then a second Prompt must fail.
	for ev := range ch {
		if _, ok := ev.(EvTurnStart); ok {
			break
		}
	}
	if err := a.Prompt(context.Background(), "nope"); !errors.Is(err, ErrBusy) {
		t.Fatalf("expected ErrBusy, got %v", err)
	}

	a.Abort()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	// After the run finishes the agent accepts prompts again.
	a2 := agenttest.New(agenttest.Turn{Text: "ok"})
	a.cfg.Stream = a2.Stream
	if err := a.Prompt(context.Background(), "again"); err != nil {
		t.Fatalf("agent stuck busy: %v", err)
	}
}

func TestAgentAbortEndsRunAborted(t *testing.T) {
	a := newTestAgent(agenttest.New(agenttest.Turn{WaitCtx: true}))
	ch, cancel := a.Subscribe()
	defer cancel()

	go func() {
		// Abort once the stream is in flight.
		time.Sleep(20 * time.Millisecond)
		a.Abort()
	}()
	go a.Prompt(context.Background(), "go")

	if reason := waitEnd(t, ch); reason != EndAborted {
		t.Fatalf("reason = %s", reason)
	}
}

func TestAgentSteerAndFollowUpQueueLimits(t *testing.T) {
	a := newTestAgent(agenttest.New())
	for i := 0; i < defaultQueueCap; i++ {
		if err := a.Steer(userMsg("s")); err != nil {
			t.Fatalf("steer %d: %v", i, err)
		}
	}
	if err := a.Steer(userMsg("overflow")); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("expected ErrQueueFull, got %v", err)
	}
	if err := a.FollowUp(userMsg("f")); err != nil {
		t.Fatal(err)
	}
}

func TestAgentRunLoopPanicRecovered(t *testing.T) {
	a := newTestAgent(agenttest.New(agenttest.Turn{Text: "ok"}))
	a.cfg.Hooks.ShouldStop = func(int, *ai.AssistantMessage) bool {
		panic("hook gone rogue")
	}
	ch, cancel := a.Subscribe()
	defer cancel()

	errc := make(chan error, 1)
	go func() { errc <- a.Prompt(context.Background(), "go") }()
	if reason := waitEnd(t, ch); reason != EndError {
		t.Fatalf("reason = %s", reason)
	}
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
	// Synthetic error message recorded; agent usable again.
	msgs := a.Messages()
	last := msgs[len(msgs)-1].(ModelMessage).Msg.(ai.AssistantMessage)
	if last.StopReason != ai.StopReasonError {
		t.Fatalf("synthetic message stopReason = %s", last.StopReason)
	}
	a.cfg.Hooks.ShouldStop = nil
	a.cfg.Stream = agenttest.New(agenttest.Turn{Text: "alive"}).Stream
	if err := a.Prompt(context.Background(), "still there?"); err != nil {
		t.Fatalf("process should survive loop panic: %v", err)
	}
}

func TestAgentMaxTurnsDefaultAndOptOut(t *testing.T) {
	if a := newTestAgent(agenttest.New()); a.maxTurns != DefaultMaxTurns {
		t.Fatalf("default maxTurns = %d", a.maxTurns)
	}
	a := newTestAgent(agenttest.New(), func(c *Config) { c.MaxTurns = -1 })
	if a.maxTurns != 0 {
		t.Fatalf("opt-out maxTurns = %d, want 0 (unlimited)", a.maxTurns)
	}
}

func TestAgentContinueResumesAfterMaxTurns(t *testing.T) {
	turns := make([]agenttest.Turn, 4)
	for i := range turns[:3] {
		turns[i] = agenttest.Turn{ToolCalls: []agenttest.ToolCallSpec{{ID: "c", Name: "probe"}}}
	}
	turns[3] = agenttest.Turn{Text: "finally done"}
	p := agenttest.New(turns...)
	a := newTestAgent(p, func(c *Config) {
		c.MaxTurns = 3
		c.Tools = registryOf(t, scriptTool{name: "probe"})
	})

	if err := a.Prompt(context.Background(), "go"); err != nil {
		t.Fatal(err)
	}
	if p.Calls() != 3 {
		t.Fatalf("first run calls = %d, want 3 (max turns)", p.Calls())
	}
	if err := a.Continue(context.Background()); err != nil {
		t.Fatal(err)
	}
	if p.Calls() != 4 {
		t.Fatalf("calls after Continue = %d, want 4", p.Calls())
	}
}

func TestAgentSubscribeReceivesRunEvents(t *testing.T) {
	a := newTestAgent(agenttest.New(agenttest.Turn{Text: "hi"}))
	ch, cancel := a.Subscribe()
	defer cancel()
	go a.Prompt(context.Background(), "hello")
	if reason := waitEnd(t, ch); reason != EndDone {
		t.Fatalf("reason = %s", reason)
	}
}

func TestRegistryStreamResolutionFailureIsErrorStream(t *testing.T) {
	ch := registryStream(context.Background(), ai.Model{API: "no-such-api"}, ai.Context{}, ai.StreamOptions{})
	var sawError bool
	for ev := range ch {
		if _, ok := ev.(ai.EventError); ok {
			sawError = true
		}
	}
	if !sawError {
		t.Fatal("expected EventError from unresolved provider")
	}
}
