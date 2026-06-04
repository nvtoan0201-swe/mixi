package agent

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/user/mixi-agent/internal/agent/agenttest"
	"github.com/user/mixi-agent/internal/ai"
	"github.com/user/mixi-agent/internal/tools"
)

func call(id, name, args string) ai.ToolCall {
	if args == "" {
		args = "{}"
	}
	return ai.ToolCall{ID: id, Name: name, Args: json.RawMessage(args)}
}

func resultText(t *testing.T, r ai.ToolResultMessage) string {
	t.Helper()
	if len(r.Content) == 0 {
		return ""
	}
	tc, ok := r.Content[0].(ai.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", r.Content[0])
	}
	return tc.Text
}

func execDeps(t *testing.T, log *eventLog, ts ...tools.Tool) *loopDeps {
	t.Helper()
	return newTestDeps(agenttest.New(), registryOf(t, ts...), log)
}

func TestSequentialExecutionOrder(t *testing.T) {
	var order []string
	var mu sync.Mutex
	seq := scriptTool{name: "seq", mode: tools.ExecSequential,
		fn: func(ctx context.Context, args json.RawMessage, _ chan<- tools.ToolUpdate) (tools.ToolResult, error) {
			mu.Lock()
			order = append(order, string(args))
			mu.Unlock()
			return tools.Text("done"), nil
		}}
	log := &eventLog{}
	d := execDeps(t, log, seq)
	results := executeToolCalls(context.Background(), d, []ai.ToolCall{
		call("c1", "seq", `{"n":1}`), call("c2", "seq", `{"n":2}`),
	})
	if len(results) != 2 || results[0].ToolCallID != "c1" || results[1].ToolCallID != "c2" {
		t.Fatalf("results = %+v", results)
	}
	want := []string{"tool_start:c1", "tool_end:c1", "tool_start:c2", "tool_end:c2"}
	if !equalSlices(log.lifecycle(), want) {
		t.Fatalf("events = %v, want %v", log.lifecycle(), want)
	}
}

func TestParallelCompletionOrderVsSourceOrder(t *testing.T) {
	delays := map[string]time.Duration{"c1": 60 * time.Millisecond, "c2": 5 * time.Millisecond, "c3": 30 * time.Millisecond}
	par := scriptTool{name: "par", mode: tools.ExecParallel,
		fn: func(ctx context.Context, args json.RawMessage, _ chan<- tools.ToolUpdate) (tools.ToolResult, error) {
			var a struct{ ID string }
			json.Unmarshal(args, &a)
			time.Sleep(delays[a.ID])
			return tools.Text("done " + a.ID), nil
		}}
	log := &eventLog{}
	d := execDeps(t, log, par)
	results := executeToolCalls(context.Background(), d, []ai.ToolCall{
		call("c1", "par", `{"ID":"c1"}`), call("c2", "par", `{"ID":"c2"}`), call("c3", "par", `{"ID":"c3"}`),
	})

	// Results: strictly source order.
	for i, want := range []string{"c1", "c2", "c3"} {
		if results[i].ToolCallID != want {
			t.Fatalf("result[%d] = %s, want %s", i, results[i].ToolCallID, want)
		}
	}
	// EvToolEnd: completion order (sleep-shuffled).
	var ends []string
	for _, ev := range log.byType("tool_end") {
		ends = append(ends, ev.(EvToolEnd).CallID)
	}
	if !equalSlices(ends, []string{"c2", "c3", "c1"}) {
		t.Fatalf("EvToolEnd order = %v, want completion order [c2 c3 c1]", ends)
	}
	// EvToolStart: source order before any execution.
	var starts []string
	for _, ev := range log.byType("tool_start") {
		starts = append(starts, ev.(EvToolStart).Call.ID)
	}
	if !equalSlices(starts, []string{"c1", "c2", "c3"}) {
		t.Fatalf("EvToolStart order = %v", starts)
	}
}

func TestMixedBatchForcesSequential(t *testing.T) {
	var active, maxActive int
	var mu sync.Mutex
	track := func(ctx context.Context, _ json.RawMessage, _ chan<- tools.ToolUpdate) (tools.ToolResult, error) {
		mu.Lock()
		active++
		if active > maxActive {
			maxActive = active
		}
		mu.Unlock()
		time.Sleep(10 * time.Millisecond)
		mu.Lock()
		active--
		mu.Unlock()
		return tools.Text("ok"), nil
	}
	seq := scriptTool{name: "seq", mode: tools.ExecSequential, fn: track}
	par := scriptTool{name: "par", mode: tools.ExecParallel, fn: track}
	d := execDeps(t, &eventLog{}, seq, par)
	executeToolCalls(context.Background(), d, []ai.ToolCall{
		call("c1", "par", ""), call("c2", "seq", ""), call("c3", "par", ""),
	})
	if maxActive != 1 {
		t.Fatalf("maxActive = %d, want 1 (sequential batch)", maxActive)
	}
}

func TestToolPanicBecomesErrorResult(t *testing.T) {
	boom := scriptTool{name: "boom", fn: func(context.Context, json.RawMessage, chan<- tools.ToolUpdate) (tools.ToolResult, error) {
		panic("kaboom")
	}}
	ok := scriptTool{name: "ok"}
	d := execDeps(t, &eventLog{}, boom, ok)
	results := executeToolCalls(context.Background(), d, []ai.ToolCall{
		call("c1", "boom", ""), call("c2", "ok", ""),
	})
	if !results[0].IsError || !strings.Contains(resultText(t, results[0]), "tool panicked: kaboom") {
		t.Fatalf("panic result = %+v", results[0])
	}
	if results[1].IsError {
		t.Fatalf("sibling call should succeed: %+v", results[1])
	}
}

func TestUnknownToolErrorResult(t *testing.T) {
	d := execDeps(t, &eventLog{})
	results := executeToolCalls(context.Background(), d, []ai.ToolCall{call("c1", "nope", "")})
	if !results[0].IsError || !strings.Contains(resultText(t, results[0]), `Unknown tool "nope"`) {
		t.Fatalf("result = %+v", results[0])
	}
}

func TestValidationFailureProducesLLMReadableError(t *testing.T) {
	strict := scriptTool{name: "strict",
		schema: `{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`}
	d := execDeps(t, &eventLog{}, strict)
	results := executeToolCalls(context.Background(), d, []ai.ToolCall{call("c1", "strict", `{"wrong":1}`)})
	text := resultText(t, results[0])
	if !results[0].IsError || !strings.Contains(text, `Validation failed for tool "strict"`) {
		t.Fatalf("validation result = %q", text)
	}
}

func TestArgumentCoercionAppliedBeforeExecute(t *testing.T) {
	var got string
	num := scriptTool{name: "num",
		schema: `{"type":"object","properties":{"n":{"type":"integer"}}}`,
		fn: func(_ context.Context, args json.RawMessage, _ chan<- tools.ToolUpdate) (tools.ToolResult, error) {
			got = string(args)
			return tools.Text("ok"), nil
		}}
	d := execDeps(t, &eventLog{}, num)
	executeToolCalls(context.Background(), d, []ai.ToolCall{call("c1", "num", `{"n":"5"}`)})
	if !strings.Contains(got, `"n":5`) {
		t.Fatalf("args not coerced: %s", got)
	}
}

func TestBeforeToolCallBlocks(t *testing.T) {
	d := execDeps(t, &eventLog{}, scriptTool{name: "rmrf"})
	d.Hooks.BeforeToolCall = func(_ context.Context, c ai.ToolCall) (*BlockDecision, error) {
		return &BlockDecision{Reason: "not allowed"}, nil
	}
	results := executeToolCalls(context.Background(), d, []ai.ToolCall{call("c1", "rmrf", "")})
	if !results[0].IsError || !strings.Contains(resultText(t, results[0]), "Tool call blocked: not allowed") {
		t.Fatalf("block result = %+v", results[0])
	}
}

func TestAfterToolCallMutatesResult(t *testing.T) {
	d := execDeps(t, &eventLog{}, scriptTool{name: "ok"})
	d.Hooks.AfterToolCall = func(_ context.Context, c ai.ToolCall, r *tools.ToolResult) error {
		r.Content = []ai.Content{ai.TextContent{Text: "overridden"}}
		return nil
	}
	results := executeToolCalls(context.Background(), d, []ai.ToolCall{call("c1", "ok", "")})
	if resultText(t, results[0]) != "overridden" {
		t.Fatalf("AfterToolCall not applied: %+v", results[0])
	}
}

func TestHookErrorsAreNoOps(t *testing.T) {
	d := execDeps(t, &eventLog{}, scriptTool{name: "ok"})
	d.Hooks.BeforeToolCall = func(context.Context, ai.ToolCall) (*BlockDecision, error) {
		return nil, context.DeadlineExceeded
	}
	d.Hooks.AfterToolCall = func(context.Context, ai.ToolCall, *tools.ToolResult) error {
		return context.DeadlineExceeded
	}
	results := executeToolCalls(context.Background(), d, []ai.ToolCall{call("c1", "ok", "")})
	if results[0].IsError {
		t.Fatalf("hook errors must be no-ops: %+v", results[0])
	}
}

func TestAbortDuringSequentialBatch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	first := scriptTool{name: "first", mode: tools.ExecSequential,
		fn: func(context.Context, json.RawMessage, chan<- tools.ToolUpdate) (tools.ToolResult, error) {
			cancel() // abort lands while the first call is executing
			return tools.Text("done"), nil
		}}
	d := execDeps(t, &eventLog{}, first)
	results := executeToolCalls(ctx, d, []ai.ToolCall{
		call("c1", "first", ""), call("c2", "first", ""), call("c3", "first", ""),
	})
	if len(results) != 3 {
		t.Fatalf("pairing broken: %d results for 3 calls", len(results))
	}
	for _, r := range results[1:] {
		if !r.IsError || resultText(t, r) != "Operation aborted" {
			t.Fatalf("queued call result = %+v", r)
		}
	}
}

func TestAbortedSequentialCallsStillEmitToolEvents(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	first := scriptTool{name: "first", mode: tools.ExecSequential,
		fn: func(context.Context, json.RawMessage, chan<- tools.ToolUpdate) (tools.ToolResult, error) {
			cancel()
			return tools.Text("done"), nil
		}}
	log := &eventLog{}
	d := execDeps(t, log, first)
	executeToolCalls(ctx, d, []ai.ToolCall{call("c1", "first", ""), call("c2", "first", "")})
	// Event stream stays symmetric with the parallel path: every call gets
	// its start/end pair even when finalized as aborted.
	want := []string{"tool_start:c1", "tool_end:c1", "tool_start:c2", "tool_end:c2"}
	if !equalSlices(log.lifecycle(), want) {
		t.Fatalf("events = %v, want %v", log.lifecycle(), want)
	}
}

func TestToolUpdatesPumpedToEvents(t *testing.T) {
	stream := scriptTool{name: "stream",
		fn: func(_ context.Context, _ json.RawMessage, updates chan<- tools.ToolUpdate) (tools.ToolResult, error) {
			updates <- tools.ToolUpdate{Content: "partial 1"}
			updates <- tools.ToolUpdate{Content: "partial 2"}
			return tools.Text("final"), nil
		}}
	log := &eventLog{}
	d := execDeps(t, log, stream)
	executeToolCalls(context.Background(), d, []ai.ToolCall{call("c1", "stream", "")})
	ups := log.byType("tool_update")
	if len(ups) != 2 {
		t.Fatalf("updates = %d, want 2", len(ups))
	}
	if u := ups[1].(EvToolUpdate); u.CallID != "c1" || u.Partial != "partial 2" {
		t.Fatalf("update = %+v", u)
	}
}
