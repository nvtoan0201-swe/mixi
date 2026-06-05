package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/user/mixi-agent/internal/ai"
	"github.com/user/mixi-agent/internal/tools"
)

// fakeFilter scripts one ToolCallFilter behavior per test.
type fakeFilter struct {
	fn func(ctx context.Context, call ai.ToolCall) (json.RawMessage, *BlockDecision, error)
}

func (f fakeFilter) FilterToolCall(ctx context.Context, call ai.ToolCall) (json.RawMessage, *BlockDecision, error) {
	return f.fn(ctx, call)
}

func TestFilterBlockReasonIsVerbatim(t *testing.T) {
	d := execDeps(t, &eventLog{}, scriptTool{name: "write"})
	d.Filters = []ToolCallFilter{fakeFilter{fn: func(_ context.Context, c ai.ToolCall) (json.RawMessage, *BlockDecision, error) {
		return c.Args, &BlockDecision{Reason: "Permission denied: blocked by deny rule write(./**)"}, nil
	}}}
	results := executeToolCalls(context.Background(), d, []ai.ToolCall{call("c1", "write", "")})
	if !results[0].IsError {
		t.Fatal("filtered call was not an error result")
	}
	got := resultText(t, results[0])
	if got != "Permission denied: blocked by deny rule write(./**)" {
		t.Fatalf("filter reason not verbatim: %q", got)
	}
}

func TestFilterRunsBeforeBeforeToolCallHook(t *testing.T) {
	var order []string
	d := execDeps(t, &eventLog{}, scriptTool{name: "ok"})
	d.Filters = []ToolCallFilter{fakeFilter{fn: func(_ context.Context, c ai.ToolCall) (json.RawMessage, *BlockDecision, error) {
		order = append(order, "filter")
		return c.Args, nil, nil
	}}}
	d.Hooks.BeforeToolCall = func(context.Context, ai.ToolCall) (*BlockDecision, error) {
		order = append(order, "hook")
		return nil, nil
	}
	executeToolCalls(context.Background(), d, []ai.ToolCall{call("c1", "ok", "")})
	if len(order) != 2 || order[0] != "filter" || order[1] != "hook" {
		t.Fatalf("order = %v, want [filter hook]", order)
	}
}

func TestFilterErrorFailsClosed(t *testing.T) {
	d := execDeps(t, &eventLog{}, scriptTool{name: "ok"})
	d.Filters = []ToolCallFilter{fakeFilter{fn: func(context.Context, ai.ToolCall) (json.RawMessage, *BlockDecision, error) {
		return nil, nil, errors.New("engine exploded")
	}}}
	results := executeToolCalls(context.Background(), d, []ai.ToolCall{call("c1", "ok", "")})
	if !results[0].IsError || !strings.Contains(resultText(t, results[0]), "engine exploded") {
		t.Fatalf("filter error did not fail closed: %+v", results[0])
	}
}

func TestFilterMayRewriteArgs(t *testing.T) {
	var seen string
	tool := scriptTool{name: "ok", fn: func(_ context.Context, args json.RawMessage, _ chan<- tools.ToolUpdate) (tools.ToolResult, error) {
		seen = string(args)
		return tools.Text("done"), nil
	}}
	d := execDeps(t, &eventLog{}, tool)
	d.Filters = []ToolCallFilter{fakeFilter{fn: func(context.Context, ai.ToolCall) (json.RawMessage, *BlockDecision, error) {
		return json.RawMessage(`{"rewritten":true}`), nil, nil
	}}}
	executeToolCalls(context.Background(), d, []ai.ToolCall{call("c1", "ok", `{"rewritten":false}`)})
	if !strings.Contains(seen, `"rewritten":true`) {
		t.Fatalf("rewritten args not used: %s", seen)
	}
}
