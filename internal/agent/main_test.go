package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"sync"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/user/mixi-agent/internal/agent/agenttest"
	"github.com/user/mixi-agent/internal/ai"
	"github.com/user/mixi-agent/internal/tools"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

// silentLog keeps expected hook/tool failures out of test output.
var silentLog = slog.New(slog.NewTextHandler(io.Discard, nil))

var testModel = ai.Model{API: "test", Provider: "test", ID: "test/scripted"}

// scriptTool is a configurable in-test tool.
type scriptTool struct {
	name   string
	mode   tools.ExecMode
	schema string
	fn     func(ctx context.Context, args json.RawMessage, updates chan<- tools.ToolUpdate) (tools.ToolResult, error)
}

func (s scriptTool) Name() string        { return s.name }
func (s scriptTool) Description() string { return "test tool " + s.name }
func (s scriptTool) Schema() json.RawMessage {
	if s.schema == "" {
		return json.RawMessage(`{"type":"object"}`)
	}
	return json.RawMessage(s.schema)
}
func (s scriptTool) Mode() tools.ExecMode { return s.mode }
func (s scriptTool) Execute(ctx context.Context, args json.RawMessage, updates chan<- tools.ToolUpdate) (tools.ToolResult, error) {
	if s.fn == nil {
		return tools.Text("ok"), nil
	}
	return s.fn(ctx, args, updates)
}

func registryOf(t *testing.T, ts ...tools.Tool) *tools.Registry {
	t.Helper()
	reg := tools.NewRegistry()
	for _, tool := range ts {
		if err := reg.Register(tool); err != nil {
			t.Fatal(err)
		}
	}
	return reg
}

// eventLog records a compact transition log for state-machine assertions.
type eventLog struct {
	mu     sync.Mutex
	names  []string
	events []Event
}

func (l *eventLog) sink(ev Event) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, ev)
	l.names = append(l.names, evName(ev))
}

func (l *eventLog) all() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.names...)
}

// lifecycle filters out render-only events (stream/tool updates), whose
// counts depend on scripted chunking, leaving the asserted state machine.
func (l *eventLog) lifecycle() []string {
	var out []string
	for _, n := range l.all() {
		if n == "msg_update" || n == "tool_update" {
			continue
		}
		out = append(out, n)
	}
	return out
}

func (l *eventLog) byType(name string) []Event {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []Event
	for i, n := range l.names {
		if n == name || (len(n) > len(name) && n[:len(name)+1] == name+":") {
			out = append(out, l.events[i])
		}
	}
	return out
}

func evName(ev Event) string {
	switch e := ev.(type) {
	case EvAgentStart:
		return "agent_start"
	case EvAgentEnd:
		return "agent_end:" + string(e.Reason)
	case EvTurnStart:
		return fmt.Sprintf("turn_start:%d", e.Turn)
	case EvTurnEnd:
		return fmt.Sprintf("turn_end:%d", e.Turn)
	case EvMessageStart:
		return "msg_start"
	case EvMessageUpdate:
		return "msg_update"
	case EvMessageEnd:
		return "msg_end"
	case EvToolStart:
		return "tool_start:" + e.Call.ID
	case EvToolUpdate:
		return "tool_update"
	case EvToolEnd:
		return "tool_end:" + e.CallID
	case EvRetryStart:
		return fmt.Sprintf("retry_start:%d", e.Attempt)
	case EvRetryEnd:
		return fmt.Sprintf("retry_end:%d:%v", e.Attempt, e.OK)
	case EvNotice:
		return "notice"
	default:
		return fmt.Sprintf("%T", ev)
	}
}

// newTestDeps wires a loopDeps around the scripted provider with fast retry.
func newTestDeps(p *agenttest.Provider, reg *tools.Registry, log *eventLog) *loopDeps {
	return &loopDeps{
		Stream:    p.Stream,
		Tools:     reg,
		Sink:      log.sink,
		Log:       silentLog,
		Model:     testModel,
		MaxTurns:  DefaultMaxTurns,
		Steering:  newBoundedQueue[AgentMessage](defaultQueueCap, DrainAll),
		FollowUp:  newBoundedQueue[AgentMessage](defaultQueueCap, DrainAll),
		Rand:      rand.New(rand.NewSource(1)),
		RetryBase: time.Millisecond,
	}
}

func userMsg(text string) AgentMessage {
	return ModelMessage{Msg: ai.UserMessage{Content: []ai.Content{ai.TextContent{Text: text}}}}
}

func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
