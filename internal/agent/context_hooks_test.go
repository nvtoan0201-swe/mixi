package agent

import (
	"context"
	"testing"

	"github.com/user/mixi-agent/internal/agent/agenttest"
	"github.com/user/mixi-agent/internal/ai"
)

func TestOnMessageSeesEveryRecordedMessage(t *testing.T) {
	p := agenttest.New(
		agenttest.Turn{ToolCalls: []agenttest.ToolCallSpec{{ID: "c1", Name: "probe"}}},
		agenttest.Turn{Text: "done"},
	)
	d := newTestDeps(p, registryOf(t, scriptTool{name: "probe"}), &eventLog{})
	var seen []string
	d.Hooks.OnMessage = func(m AgentMessage) {
		mm := m.(ModelMessage)
		seen = append(seen, string(mm.Msg.MsgRole()))
	}

	runLoop(context.Background(), d, nil, []AgentMessage{userMsg("go")})
	want := []string{"user", "assistant", "toolResult", "assistant"}
	if !equalSlices(seen, want) {
		t.Fatalf("OnMessage order: got %v, want %v", seen, want)
	}
}

func TestAfterTurnRunsPerTurn(t *testing.T) {
	p := agenttest.New(
		agenttest.Turn{ToolCalls: []agenttest.ToolCallSpec{{ID: "c1", Name: "probe"}}},
		agenttest.Turn{Text: "done"},
	)
	d := newTestDeps(p, registryOf(t, scriptTool{name: "probe"}), &eventLog{})
	var turns []int
	d.Hooks.AfterTurn = func(_ context.Context, turn int) error {
		turns = append(turns, turn)
		return nil
	}

	_, reason := runLoop(context.Background(), d, nil, []AgentMessage{userMsg("go")})
	if reason != EndDone {
		t.Fatalf("reason = %s", reason)
	}
	if len(turns) != 2 || turns[0] != 1 || turns[1] != 2 {
		t.Fatalf("AfterTurn turns = %v, want [1 2]", turns)
	}
}

func TestOverflowRecoveryRetriesOnceAfterHook(t *testing.T) {
	p := agenttest.New(
		agenttest.Turn{Err: "HTTP 400: prompt is too long: 250000 tokens > 200000"},
		agenttest.Turn{Text: "recovered"},
	)
	log := &eventLog{}
	d := newTestDeps(p, registryOf(t), log)
	freed := 0
	d.Hooks.OnContextOverflow = func(context.Context) bool {
		freed++
		return true
	}

	msgs, reason := runLoop(context.Background(), d, nil, []AgentMessage{userMsg("go")})
	if reason != EndDone {
		t.Fatalf("reason = %s", reason)
	}
	if freed != 1 {
		t.Fatalf("overflow hook ran %d times, want 1", freed)
	}
	// The failed assistant message was dropped: only user + recovered remain.
	if len(msgs) != 2 {
		t.Fatalf("messages = %d, want 2 (failed turn discarded)", len(msgs))
	}
	last := msgs[1].(ModelMessage).Msg.(ai.AssistantMessage)
	if last.StopReason != ai.StopReasonStop || last.ErrorMessage != "" {
		t.Fatalf("recovered turn = %+v", last)
	}
}

func TestOverflowRecoveryOnlyOnce(t *testing.T) {
	p := agenttest.New(
		agenttest.Turn{Err: "HTTP 400: prompt is too long"},
		agenttest.Turn{Err: "HTTP 400: prompt is too long"},
	)
	d := newTestDeps(p, registryOf(t), &eventLog{})
	freed := 0
	d.Hooks.OnContextOverflow = func(context.Context) bool {
		freed++
		return true
	}

	_, reason := runLoop(context.Background(), d, nil, []AgentMessage{userMsg("go")})
	if reason != EndError {
		t.Fatalf("reason = %s, want error after second overflow", reason)
	}
	if freed != 1 {
		t.Fatalf("overflow hook ran %d times, want exactly 1 (no recursion)", freed)
	}
}

func TestOverflowHookDecliningEndsRun(t *testing.T) {
	p := agenttest.New(agenttest.Turn{Err: "HTTP 400: prompt is too long"})
	d := newTestDeps(p, registryOf(t), &eventLog{})
	d.Hooks.OnContextOverflow = func(context.Context) bool { return false }

	_, reason := runLoop(context.Background(), d, nil, []AgentMessage{userMsg("go")})
	if reason != EndError {
		t.Fatalf("reason = %s, want error when hook cannot free context", reason)
	}
	if p.Calls() != 1 {
		t.Fatalf("provider calls = %d, want 1 (no blind retry)", p.Calls())
	}
}

func TestScriptedUsageSurfacesOnFinalMessage(t *testing.T) {
	p := agenttest.New(agenttest.Turn{Text: "hi", Usage: ai.Usage{Total: 12345}})
	d := newTestDeps(p, registryOf(t), &eventLog{})

	msgs, _ := runLoop(context.Background(), d, nil, []AgentMessage{userMsg("go")})
	asst := msgs[1].(ModelMessage).Msg.(ai.AssistantMessage)
	if asst.Usage.Total != 12345 {
		t.Fatalf("usage total = %d, want scripted 12345", asst.Usage.Total)
	}
}
