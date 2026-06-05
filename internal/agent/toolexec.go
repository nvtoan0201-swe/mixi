package agent

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"time"

	"github.com/user/mixi-agent/internal/ai"
	"github.com/user/mixi-agent/internal/schema"
	"github.com/user/mixi-agent/internal/tools"
)

const abortedResultText = "Operation aborted"

// executeToolCalls dispatches one batch of model-issued calls and returns
// results in source order (one per call, pairing always preserved).
// Batch mode is parallel unless the config forces sequential or any call's
// tool declares ExecSequential.
func executeToolCalls(ctx context.Context, d *loopDeps, calls []ai.ToolCall) []ai.ToolResultMessage {
	if len(calls) == 0 {
		return nil
	}
	sequential := d.Sequential
	for _, c := range calls {
		if t, ok := d.Tools.Get(c.Name); ok && t.Mode() == tools.ExecSequential {
			sequential = true
			break
		}
	}
	if sequential {
		return execSequential(ctx, d, calls)
	}
	return execParallel(ctx, d, calls)
}

// execSequential runs calls one at a time in source order, emitting each
// result as soon as it completes.
func execSequential(ctx context.Context, d *loopDeps, calls []ai.ToolCall) []ai.ToolResultMessage {
	results := make([]ai.ToolResultMessage, 0, len(calls))
	aborted := false
	for _, call := range calls {
		d.emit(EvToolStart{Call: call})
		var res ai.ToolResultMessage
		if aborted || ctx.Err() != nil {
			aborted = true
			res = errorResult(call, abortedResultText)
		} else {
			res = runOneCall(ctx, d, call)
		}
		d.emit(EvToolEnd{CallID: call.ID, Result: res})
		results = append(results, res)
	}
	return results
}

// execParallel validates and filters every call in source order first
// (immediate failures finalized synchronously), then runs the remainder in
// goroutines. EvToolEnd fires in completion order; the returned slice is in
// source order — event order ≠ message order by design.
func execParallel(ctx context.Context, d *loopDeps, calls []ai.ToolCall) []ai.ToolResultMessage {
	results := make([]ai.ToolResultMessage, len(calls))
	type job struct {
		idx  int
		call ai.ToolCall
		tool tools.Tool
		args ai.ToolCall // validated args
	}
	var jobs []job

	for i, call := range calls {
		d.emit(EvToolStart{Call: call})
		if ctx.Err() != nil {
			results[i] = errorResult(call, abortedResultText)
			d.emit(EvToolEnd{CallID: call.ID, Result: results[i]})
			continue
		}
		tool, prepared, errRes := prepareCall(ctx, d, call)
		if errRes != nil {
			results[i] = *errRes
			d.emit(EvToolEnd{CallID: call.ID, Result: results[i]})
			continue
		}
		jobs = append(jobs, job{idx: i, call: call, tool: tool, args: prepared})
	}

	var wg sync.WaitGroup
	for _, j := range jobs {
		wg.Add(1)
		go func(j job) {
			defer wg.Done()
			var res ai.ToolResultMessage
			if ctx.Err() != nil {
				res = errorResult(j.call, abortedResultText)
			} else {
				res = invokeTool(ctx, d, j.tool, j.args)
			}
			results[j.idx] = res
			d.emit(EvToolEnd{CallID: j.call.ID, Result: res})
		}(j)
	}
	wg.Wait()
	return results
}

// runOneCall is the full sequential path for one call: prepare + invoke.
func runOneCall(ctx context.Context, d *loopDeps, call ai.ToolCall) ai.ToolResultMessage {
	tool, prepared, errRes := prepareCall(ctx, d, call)
	if errRes != nil {
		return *errRes
	}
	if ctx.Err() != nil {
		return errorResult(call, abortedResultText)
	}
	return invokeTool(ctx, d, tool, prepared)
}

// prepareCall resolves the tool, runs the filter pipeline (permission
// engine, extension host) and the BeforeToolCall hook, then
// validates+coerces arguments. A non-nil result is a finalized failure.
func prepareCall(ctx context.Context, d *loopDeps, call ai.ToolCall) (tools.Tool, ai.ToolCall, *ai.ToolResultMessage) {
	tool, ok := d.Tools.Get(call.Name)
	if !ok {
		r := errorResult(call, fmt.Sprintf("Unknown tool %q", call.Name))
		return nil, call, &r
	}
	for _, f := range d.Filters {
		args, block, err := f.FilterToolCall(ctx, call)
		if err != nil {
			// A broken filter must fail closed: letting the call through
			// would turn a filter bug into a permission bypass.
			r := errorResult(call, fmt.Sprintf("Tool call rejected: filter failed: %v", err))
			return nil, call, &r
		}
		if block != nil {
			r := errorResult(call, block.Reason)
			return nil, call, &r
		}
		if args != nil {
			call.Args = args
		}
	}
	if block := d.Hooks.beforeToolCall(ctx, d.Log, call); block != nil {
		r := errorResult(call, "Tool call blocked: "+block.Reason)
		return nil, call, &r
	}
	v, err := schema.Compile(tool.Schema())
	if err != nil {
		r := errorResult(call, fmt.Sprintf("Tool %q has an invalid schema: %v", call.Name, err))
		return nil, call, &r
	}
	coerced, err := v.ValidateAndCoerce(call.Name, call.Args)
	if err != nil {
		// LLM-readable validation report; the model self-corrects next turn.
		r := errorResult(call, err.Error())
		return nil, call, &r
	}
	prepared := call
	prepared.Args = coerced
	return tool, prepared, nil
}

// invokeTool executes one prepared call with panic recovery, partial-output
// pumping, and the AfterToolCall hook.
func invokeTool(ctx context.Context, d *loopDeps, tool tools.Tool, call ai.ToolCall) ai.ToolResultMessage {
	from := time.Now()
	updates := make(chan tools.ToolUpdate, 16)
	var pumpWG sync.WaitGroup
	pumpWG.Add(1)
	go func() {
		defer pumpWG.Done()
		for u := range updates {
			d.emit(EvToolUpdate{CallID: call.ID, Partial: u.Content})
		}
	}()

	res, err := safeExecute(ctx, d, tool, call, updates)
	close(updates)
	pumpWG.Wait()

	if err != nil {
		res = tools.Errorf("%s", err.Error())
	}
	d.Hooks.afterToolCall(ctx, d.Log, call, &res)
	d.Log.Info("agent: tool exec", "tool", call.Name, "call_id", call.ID,
		"is_error", res.IsError, "latency_ms", time.Since(from).Milliseconds())
	return ai.ToolResultMessage{
		ToolCallID: call.ID,
		ToolName:   call.Name,
		Content:    res.Content,
		Details:    res.Details,
		IsError:    res.IsError,
		Timestamp:  time.Now().UnixMilli(),
	}
}

// safeExecute wraps Tool.Execute so a panicking tool becomes an IsError
// result instead of killing the process. The stack is logged at ERROR and
// its top frames are surfaced to the model.
func safeExecute(ctx context.Context, d *loopDeps, tool tools.Tool, call ai.ToolCall, updates chan<- tools.ToolUpdate) (res tools.ToolResult, err error) {
	defer func() {
		if r := recover(); r != nil {
			buf := make([]byte, 16*1024)
			stack := string(buf[:runtime.Stack(buf, false)])
			d.Log.Error("agent: tool panicked", "tool", call.Name, "panic", r, "stack", stack)
			res = tools.Errorf("tool panicked: %v\n%s", r, topFrames(stack, 5))
			err = nil
		}
	}()
	return tool.Execute(ctx, call.Args, updates)
}

// topFrames trims a runtime stack to its first n call sites (2 lines each
// after the goroutine header).
func topFrames(stack string, n int) string {
	lines := splitLines(stack)
	limit := 1 + 2*n // header + n frames
	if len(lines) > limit {
		lines = lines[:limit]
	}
	out := ""
	for i, l := range lines {
		if i > 0 {
			out += "\n"
		}
		out += l
	}
	return out
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

// errorResult builds the IsError tool result that keeps tool_use/tool_result
// pairing valid for the provider.
func errorResult(call ai.ToolCall, text string) ai.ToolResultMessage {
	return ai.ToolResultMessage{
		ToolCallID: call.ID,
		ToolName:   call.Name,
		Content:    []ai.Content{ai.TextContent{Text: text}},
		IsError:    true,
		Timestamp:  time.Now().UnixMilli(),
	}
}
