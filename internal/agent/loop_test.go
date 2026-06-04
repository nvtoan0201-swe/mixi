package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/user/mixi-agent/internal/agent/agenttest"
	"github.com/user/mixi-agent/internal/ai"
	"github.com/user/mixi-agent/internal/tools"
)

func TestSingleTurnTransitions(t *testing.T) {
	p := agenttest.New(agenttest.Turn{Text: "hello"})
	log := &eventLog{}
	d := newTestDeps(p, registryOf(t), log)

	msgs, reason := runLoop(context.Background(), d, nil, []AgentMessage{userMsg("hi")})
	if reason != EndDone {
		t.Fatalf("reason = %s", reason)
	}
	want := []string{
		"agent_start",
		"msg_start", "msg_end", // injected user prompt
		"turn_start:1",
		"msg_start", "msg_end", // assistant stream
		"turn_end:1",
		"agent_end:done",
	}
	if !equalSlices(log.lifecycle(), want) {
		t.Fatalf("transitions:\n got %v\nwant %v", log.lifecycle(), want)
	}
	if len(msgs) != 2 { // user + assistant
		t.Fatalf("new messages = %d", len(msgs))
	}
}

func TestMultiTurnToolLoop(t *testing.T) {
	p := agenttest.New(
		agenttest.Turn{Text: "let me check", ToolCalls: []agenttest.ToolCallSpec{{ID: "c1", Name: "probe"}}},
		agenttest.Turn{Text: "all done"},
	)
	log := &eventLog{}
	d := newTestDeps(p, registryOf(t, scriptTool{name: "probe"}), log)

	msgs, reason := runLoop(context.Background(), d, nil, []AgentMessage{userMsg("check")})
	if reason != EndDone {
		t.Fatalf("reason = %s", reason)
	}
	want := []string{
		"agent_start",
		"msg_start", "msg_end",
		"turn_start:1", "msg_start", "msg_end",
		"tool_start:c1", "tool_end:c1",
		"turn_end:1",
		"turn_start:2", "msg_start", "msg_end",
		"turn_end:2",
		"agent_end:done",
	}
	if !equalSlices(log.lifecycle(), want) {
		t.Fatalf("transitions:\n got %v\nwant %v", log.lifecycle(), want)
	}
	// user, assistant(toolUse), toolResult, assistant(stop)
	if len(msgs) != 4 {
		t.Fatalf("new messages = %d", len(msgs))
	}
	// Second call's context must contain the tool result.
	if calls := p.Calls(); calls != 2 {
		t.Fatalf("provider calls = %d", calls)
	}
	last := p.Contexts[1].Messages[len(p.Contexts[1].Messages)-1]
	if _, ok := last.(ai.ToolResultMessage); !ok {
		t.Fatalf("turn-2 context tail = %T, want ToolResultMessage", last)
	}
}

func TestMaxTurnsStopsRun(t *testing.T) {
	// Script always asks for another tool call: would loop forever.
	turns := make([]agenttest.Turn, 10)
	for i := range turns {
		turns[i] = agenttest.Turn{ToolCalls: []agenttest.ToolCallSpec{{ID: "c", Name: "probe"}}}
	}
	p := agenttest.New(turns...)
	log := &eventLog{}
	d := newTestDeps(p, registryOf(t, scriptTool{name: "probe"}), log)
	d.MaxTurns = 2

	_, reason := runLoop(context.Background(), d, nil, []AgentMessage{userMsg("go")})
	if reason != EndMaxTurns {
		t.Fatalf("reason = %s", reason)
	}
	if p.Calls() != 2 {
		t.Fatalf("provider calls = %d, want 2", p.Calls())
	}
	if len(log.byType("notice")) != 1 {
		t.Fatal("expected EvNotice on max-turns")
	}
	tail := log.lifecycle()
	if tail[len(tail)-1] != "agent_end:maxTurns" || tail[len(tail)-2] != "notice" {
		t.Fatalf("tail = %v", tail[len(tail)-3:])
	}
}

func TestMaxTurnsZeroIsUnlimited(t *testing.T) {
	turns := make([]agenttest.Turn, 90)
	for i := range turns[:89] {
		turns[i] = agenttest.Turn{ToolCalls: []agenttest.ToolCallSpec{{ID: "c", Name: "probe"}}}
	}
	turns[89] = agenttest.Turn{Text: "done"}
	p := agenttest.New(turns...)
	d := newTestDeps(p, registryOf(t, scriptTool{name: "probe"}), &eventLog{})
	d.MaxTurns = 0

	_, reason := runLoop(context.Background(), d, nil, []AgentMessage{userMsg("go")})
	if reason != EndDone || p.Calls() != 90 {
		t.Fatalf("reason = %s, calls = %d", reason, p.Calls())
	}
}

func TestSteerDuringToolExecutionTriggersTurn(t *testing.T) {
	p := agenttest.New(
		agenttest.Turn{ToolCalls: []agenttest.ToolCallSpec{{ID: "c1", Name: "probe"}}},
		agenttest.Turn{Text: "saw your steer"},
		agenttest.Turn{Text: "final"},
	)
	log := &eventLog{}
	var d *loopDeps
	steerer := scriptTool{name: "probe", fn: func(context.Context, json.RawMessage, chan<- tools.ToolUpdate) (tools.ToolResult, error) {
		d.Steering.Push(userMsg("steer!"))
		return tools.Text("ok"), nil
	}}
	d = newTestDeps(p, registryOf(t, steerer), log)

	_, reason := runLoop(context.Background(), d, nil, []AgentMessage{userMsg("go")})
	if reason != EndDone {
		t.Fatalf("reason = %s", reason)
	}
	// Turn 2 must include the injected steering message in its context.
	if p.Calls() != 2 {
		t.Fatalf("provider calls = %d, want 2", p.Calls())
	}
	ctx2 := p.Contexts[1].Messages
	found := false
	for _, m := range ctx2 {
		if um, ok := m.(ai.UserMessage); ok {
			if tc, ok := um.Content[0].(ai.TextContent); ok && tc.Text == "steer!" {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("steering message not injected into turn-2 context")
	}
}

func TestSteerAfterToollessTurnTriggersAnotherTurn(t *testing.T) {
	p := agenttest.New(
		agenttest.Turn{Text: "first answer"},
		agenttest.Turn{Text: "answer to steer"},
	)
	log := &eventLog{}
	d := newTestDeps(p, registryOf(t), log)
	// PrepareNextTurn runs after turn 1 ends, before the steering check —
	// simulates a steer arriving while the turn was streaming.
	d.Hooks.PrepareNextTurn = func(turn int) *TurnUpdate {
		if turn == 1 {
			d.Steering.Push(userMsg("wait, also do X"))
		}
		return nil
	}

	_, reason := runLoop(context.Background(), d, nil, []AgentMessage{userMsg("go")})
	if reason != EndDone || p.Calls() != 2 {
		t.Fatalf("reason = %s, calls = %d, want 2", reason, p.Calls())
	}
}

func TestFollowUpExtendsRun(t *testing.T) {
	p := agenttest.New(
		agenttest.Turn{Text: "first"},
		agenttest.Turn{Text: "second"},
	)
	log := &eventLog{}
	d := newTestDeps(p, registryOf(t), log)
	d.FollowUp.Push(userMsg("and another thing"))

	_, reason := runLoop(context.Background(), d, nil, []AgentMessage{userMsg("go")})
	if reason != EndDone || p.Calls() != 2 {
		t.Fatalf("reason = %s, calls = %d", reason, p.Calls())
	}
	want := []string{
		"agent_start",
		"msg_start", "msg_end",
		"turn_start:1", "msg_start", "msg_end", "turn_end:1",
		"msg_start", "msg_end", // follow-up injected
		"turn_start:2", "msg_start", "msg_end", "turn_end:2",
		"agent_end:done",
	}
	if !equalSlices(log.lifecycle(), want) {
		t.Fatalf("transitions:\n got %v\nwant %v", log.lifecycle(), want)
	}
}

func TestEmptyInitialForcesOneTurn(t *testing.T) {
	p := agenttest.New(agenttest.Turn{Text: "resumed"})
	d := newTestDeps(p, registryOf(t), &eventLog{})
	_, reason := runLoop(context.Background(), d, []AgentMessage{userMsg("old context")}, nil)
	if reason != EndDone || p.Calls() != 1 {
		t.Fatalf("reason = %s, calls = %d", reason, p.Calls())
	}
}

func TestTransientErrorRetriedThenSucceeds(t *testing.T) {
	p := agenttest.New(
		agenttest.Turn{Err: "anthropic: HTTP 429: rate limited"},
		agenttest.Turn{Text: "recovered"},
	)
	log := &eventLog{}
	d := newTestDeps(p, registryOf(t), log)

	_, reason := runLoop(context.Background(), d, nil, []AgentMessage{userMsg("go")})
	if reason != EndDone || p.Calls() != 2 {
		t.Fatalf("reason = %s, calls = %d", reason, p.Calls())
	}
	if len(log.byType("retry_start")) != 1 {
		t.Fatal("expected one EvRetryStart")
	}
	ends := log.byType("retry_end")
	if len(ends) != 1 || !ends[0].(EvRetryEnd).OK {
		t.Fatalf("retry_end = %+v", ends)
	}
}

func TestRetriesExhaustedSurfacesError(t *testing.T) {
	turns := make([]agenttest.Turn, 6)
	for i := range turns {
		turns[i] = agenttest.Turn{Err: "HTTP 503: unavailable"}
	}
	p := agenttest.New(turns...)
	log := &eventLog{}
	d := newTestDeps(p, registryOf(t), log)

	msgs, reason := runLoop(context.Background(), d, nil, []AgentMessage{userMsg("go")})
	if reason != EndError {
		t.Fatalf("reason = %s", reason)
	}
	if p.Calls() != retryMaxAttempts+1 {
		t.Fatalf("calls = %d, want %d", p.Calls(), retryMaxAttempts+1)
	}
	ends := log.byType("retry_end")
	if len(ends) != 1 || ends[0].(EvRetryEnd).OK {
		t.Fatalf("retry_end = %+v", ends)
	}
	// The failed assistant message is still recorded for persistence.
	last := msgs[len(msgs)-1].(ModelMessage).Msg.(ai.AssistantMessage)
	if last.StopReason != ai.StopReasonError {
		t.Fatalf("last message stopReason = %s", last.StopReason)
	}
}

func TestPartialContentStreamNotRetried(t *testing.T) {
	p := agenttest.New(agenttest.Turn{PartialText: "I was saying", Err: "HTTP 500: hiccup"})
	log := &eventLog{}
	d := newTestDeps(p, registryOf(t), log)

	_, reason := runLoop(context.Background(), d, nil, []AgentMessage{userMsg("go")})
	if reason != EndError {
		t.Fatalf("reason = %s", reason)
	}
	if p.Calls() != 1 {
		t.Fatalf("calls = %d, want 1 (no retry after partial content)", p.Calls())
	}
	if len(log.byType("retry_start")) != 0 {
		t.Fatal("partial-content stream must not retry")
	}
}

func TestOverflowErrorNotRetried(t *testing.T) {
	p := agenttest.New(agenttest.Turn{Err: "HTTP 400: prompt is too long: 250000 tokens > 200000"})
	d := newTestDeps(p, registryOf(t), &eventLog{})

	_, reason := runLoop(context.Background(), d, nil, []AgentMessage{userMsg("go")})
	if reason != EndError || p.Calls() != 1 {
		t.Fatalf("reason = %s, calls = %d", reason, p.Calls())
	}
}

func TestFatalErrorEndsRunImmediately(t *testing.T) {
	p := agenttest.New(agenttest.Turn{Err: "anthropic: HTTP 401: invalid api key"})
	log := &eventLog{}
	d := newTestDeps(p, registryOf(t), log)

	_, reason := runLoop(context.Background(), d, nil, []AgentMessage{userMsg("go")})
	if reason != EndError || p.Calls() != 1 {
		t.Fatalf("reason = %s, calls = %d", reason, p.Calls())
	}
	tail := log.lifecycle()
	if tail[len(tail)-1] != "agent_end:error" {
		t.Fatalf("tail = %v", tail)
	}
}

func TestAbortDuringStream(t *testing.T) {
	p := agenttest.New(agenttest.Turn{WaitCtx: true})
	log := &eventLog{}
	d := newTestDeps(p, registryOf(t), log)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	_, reason := runLoop(ctx, d, nil, []AgentMessage{userMsg("go")})
	if reason != EndAborted {
		t.Fatalf("reason = %s", reason)
	}
	tail := log.lifecycle()
	if tail[len(tail)-1] != "agent_end:aborted" {
		t.Fatalf("tail = %v", tail)
	}
}

func TestAbortDuringToolsEndsRunAfterTurnEnd(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	p := agenttest.New(agenttest.Turn{ToolCalls: []agenttest.ToolCallSpec{
		{ID: "c1", Name: "aborter"}, {ID: "c2", Name: "aborter"},
	}})
	log := &eventLog{}
	aborter := scriptTool{name: "aborter", mode: tools.ExecSequential,
		fn: func(context.Context, json.RawMessage, chan<- tools.ToolUpdate) (tools.ToolResult, error) {
			cancel()
			return tools.Text("ok"), nil
		}}
	d := newTestDeps(p, registryOf(t, aborter), log)

	msgs, reason := runLoop(ctx, d, nil, []AgentMessage{userMsg("go")})
	if reason != EndAborted {
		t.Fatalf("reason = %s", reason)
	}
	// Pairing: both calls have results; second is "Operation aborted".
	var results []ai.ToolResultMessage
	for _, m := range msgs {
		if mm, ok := m.(ModelMessage); ok {
			if tr, ok := mm.Msg.(ai.ToolResultMessage); ok {
				results = append(results, tr)
			}
		}
	}
	if len(results) != 2 || !results[1].IsError {
		t.Fatalf("results = %+v", results)
	}
	// EvTurnEnd still fires before agent_end so consumers see a closed turn.
	tail := log.lifecycle()
	if tail[len(tail)-2] != "turn_end:1" || tail[len(tail)-1] != "agent_end:aborted" {
		t.Fatalf("tail = %v", tail[len(tail)-3:])
	}
}

func TestShouldStopHookEndsRun(t *testing.T) {
	p := agenttest.New(
		agenttest.Turn{ToolCalls: []agenttest.ToolCallSpec{{ID: "c1", Name: "probe"}}},
		agenttest.Turn{Text: "never streamed"},
	)
	d := newTestDeps(p, registryOf(t, scriptTool{name: "probe"}), &eventLog{})
	d.Hooks.ShouldStop = func(turn int, last *ai.AssistantMessage) bool { return true }

	_, reason := runLoop(context.Background(), d, nil, []AgentMessage{userMsg("go")})
	if reason != EndDone || p.Calls() != 1 {
		t.Fatalf("reason = %s, calls = %d (ShouldStop must preempt turn 2)", reason, p.Calls())
	}
}

func TestPrepareNextTurnSwapsModel(t *testing.T) {
	p := agenttest.New(
		agenttest.Turn{ToolCalls: []agenttest.ToolCallSpec{{ID: "c1", Name: "probe"}}},
		agenttest.Turn{Text: "done"},
	)
	log := &eventLog{}
	d := newTestDeps(p, registryOf(t, scriptTool{name: "probe"}), log)
	swapped := ai.Model{API: "test", Provider: "test", ID: "test/bigger"}
	d.Hooks.PrepareNextTurn = func(turn int) *TurnUpdate { return &TurnUpdate{Model: &swapped} }

	runLoop(context.Background(), d, nil, []AgentMessage{userMsg("go")})
	// The swapped model shows up on turn 2's assistant message.
	var lastModel string
	for _, ev := range log.byType("msg_end") {
		if mm, ok := ev.(EvMessageEnd).Msg.(ModelMessage); ok {
			if am, ok := mm.Msg.(ai.AssistantMessage); ok {
				lastModel = am.Model
			}
		}
	}
	if lastModel != "test/bigger" {
		t.Fatalf("turn-2 model = %q, want test/bigger", lastModel)
	}
}

func TestTransformContextHookErrorIsNoOp(t *testing.T) {
	p := agenttest.New(agenttest.Turn{Text: "fine"})
	d := newTestDeps(p, registryOf(t), &eventLog{})
	d.Hooks.TransformContext = func(_ context.Context, msgs []AgentMessage) ([]AgentMessage, error) {
		return nil, context.DeadlineExceeded
	}
	_, reason := runLoop(context.Background(), d, nil, []AgentMessage{userMsg("go")})
	if reason != EndDone {
		t.Fatalf("hook error must not abort run: %s", reason)
	}
	// Original context still sent (hook result discarded on error).
	if len(p.Contexts[0].Messages) != 1 {
		t.Fatalf("context messages = %d, want 1", len(p.Contexts[0].Messages))
	}
}

func TestTransformContextHookRewritesContext(t *testing.T) {
	p := agenttest.New(agenttest.Turn{Text: "fine"})
	d := newTestDeps(p, registryOf(t), &eventLog{})
	d.Hooks.TransformContext = func(_ context.Context, msgs []AgentMessage) ([]AgentMessage, error) {
		return append(msgs, userMsg("injected by hook")), nil
	}
	runLoop(context.Background(), d, nil, []AgentMessage{userMsg("go")})
	if len(p.Contexts[0].Messages) != 2 {
		t.Fatalf("context messages = %d, want 2", len(p.Contexts[0].Messages))
	}
}

func TestGetAPIKeyHookAppliedPerCall(t *testing.T) {
	p := agenttest.New(agenttest.Turn{Text: "ok"})
	d := newTestDeps(p, registryOf(t), &eventLog{})
	var asked string
	d.Hooks.GetAPIKey = func(provider string) (string, error) {
		asked = provider
		return "sk-test", nil
	}
	runLoop(context.Background(), d, nil, []AgentMessage{userMsg("go")})
	if asked != "test" {
		t.Fatalf("GetAPIKey provider = %q", asked)
	}
}

func TestSystemPromptAndToolDefsSentToProvider(t *testing.T) {
	p := agenttest.New(agenttest.Turn{Text: "ok"})
	d := newTestDeps(p, registryOf(t, scriptTool{name: "probe"}), &eventLog{})
	d.SystemPrompt = "you are a test"
	runLoop(context.Background(), d, nil, []AgentMessage{userMsg("go")})
	c := p.Contexts[0]
	if c.SystemPrompt != "you are a test" {
		t.Fatalf("system prompt = %q", c.SystemPrompt)
	}
	if len(c.Tools) != 1 || c.Tools[0].Name != "probe" {
		t.Fatalf("tools = %+v", c.Tools)
	}
}

func TestExcludedResultsDroppedFromLLMContext(t *testing.T) {
	p := agenttest.New(agenttest.Turn{Text: "ok"})
	d := newTestDeps(p, registryOf(t), &eventLog{})
	history := []AgentMessage{
		userMsg("old"),
		ModelMessage{Msg: ai.ToolResultMessage{ToolCallID: "x", ToolName: "read"}, Excluded: true},
	}
	runLoop(context.Background(), d, history, []AgentMessage{userMsg("new")})
	for _, m := range p.Contexts[0].Messages {
		if _, ok := m.(ai.ToolResultMessage); ok {
			t.Fatal("excluded tool result reached the provider")
		}
	}
}

func TestStreamWithoutTerminalEventIsErrorTurn(t *testing.T) {
	d := newTestDeps(agenttest.New(), registryOf(t), &eventLog{})
	d.Stream = func(context.Context, ai.Model, ai.Context, ai.StreamOptions) <-chan ai.StreamEvent {
		ch := make(chan ai.StreamEvent)
		close(ch) // contract violation: no Done/Error
		return ch
	}
	msgs, reason := runLoop(context.Background(), d, nil, []AgentMessage{userMsg("go")})
	if reason != EndError {
		t.Fatalf("reason = %s", reason)
	}
	last := msgs[len(msgs)-1].(ModelMessage).Msg.(ai.AssistantMessage)
	if !strings.Contains(last.ErrorMessage, "without Done/Error") {
		t.Fatalf("error = %q", last.ErrorMessage)
	}
}
