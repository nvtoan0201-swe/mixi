package obs

import (
	"strings"
	"testing"
	"time"

	"github.com/user/mixi-agent/internal/agent"
	"github.com/user/mixi-agent/internal/ai"
)

func assistantEnd(provider, model string, u ai.Usage) agent.Event {
	return agent.EvMessageEnd{Msg: agent.ModelMessage{Msg: ai.AssistantMessage{
		Provider: provider, Model: model, Usage: u,
	}}}
}

func usage(in, out int, cost float64) ai.Usage {
	return ai.Usage{Input: in, Output: out, Total: in + out, Cost: ai.Cost{Total: cost}}
}

// TestTrackerFoldsRun: a scripted two-turn run lands per-turn records, tool
// counts, totals, per-model split, and the last context size.
func TestTrackerFoldsRun(t *testing.T) {
	tr := NewTracker()
	clock := time.Unix(0, 0)
	tr.now = func() time.Time { clock = clock.Add(250 * time.Millisecond); return clock }

	for _, ev := range []agent.Event{
		agent.EvAgentStart{},
		agent.EvTurnStart{Turn: 1},
		assistantEnd("anthropic", "claude-sonnet-4-6", usage(100, 10, 0.01)),
		agent.EvToolStart{Call: ai.ToolCall{ID: "c1", Name: "bash"}},
		agent.EvToolStart{Call: ai.ToolCall{ID: "c2", Name: "read"}},
		agent.EvTurnEnd{Turn: 1},
		agent.EvTurnStart{Turn: 2},
		assistantEnd("faux", "scripted", usage(200, 20, 0.02)),
		agent.EvTurnEnd{Turn: 2},
		agent.EvAgentEnd{Reason: agent.EndDone},
	} {
		tr.Observe(ev)
	}

	snap := tr.Snapshot()
	if len(snap.Turns) != 2 {
		t.Fatalf("turns = %d, want 2", len(snap.Turns))
	}
	if snap.Turns[0].ToolCalls != 2 || snap.Turns[1].ToolCalls != 0 {
		t.Errorf("tool counts = %d,%d want 2,0", snap.Turns[0].ToolCalls, snap.Turns[1].ToolCalls)
	}
	if snap.Turns[0].Model != "anthropic/claude-sonnet-4-6" || snap.Turns[0].DurMs <= 0 {
		t.Errorf("turn 1 = %+v", snap.Turns[0])
	}
	if snap.Total.Input != 300 || snap.Total.Output != 30 || snap.Total.Total != 330 {
		t.Errorf("total = %+v", snap.Total)
	}
	if snap.Total.Cost.Total != 0.03 {
		t.Errorf("cost = %v, want 0.03", snap.Total.Cost.Total)
	}
	if len(snap.ByModel) != 2 || snap.ByModel["faux/scripted"].Input != 200 {
		t.Errorf("byModel = %+v", snap.ByModel)
	}
	if snap.LastContext != 220 {
		t.Errorf("lastContext = %d, want 220", snap.LastContext)
	}
}

// TestSnapshotConsistency: the figures every surface (/cost, --print-stats)
// derives from Snapshot equal the plain sum of the recorded message usage.
func TestSnapshotConsistency(t *testing.T) {
	usages := []ai.Usage{usage(11, 3, 0.001), usage(29, 7, 0.002), usage(53, 13, 0.004)}
	tr := NewTracker()
	var want ai.Usage
	for i, u := range usages {
		tr.Observe(agent.EvTurnStart{Turn: i + 1})
		tr.Observe(assistantEnd("anthropic", "claude-sonnet-4-6", u))
		tr.Observe(agent.EvTurnEnd{Turn: i + 1})
		addUsage(&want, u)
	}
	snap := tr.Snapshot()
	if snap.Total != want {
		t.Fatalf("snapshot total %+v != message-usage sum %+v", snap.Total, want)
	}
	sum := snap.Summary()
	for _, frag := range []string{"turns=3", "input=93", "output=23", "total=116", "cost=$0.0070"} {
		if !strings.Contains(sum, frag) {
			t.Errorf("Summary() = %q, missing %q", sum, frag)
		}
	}
}

// TestSnapshotIncludesInFlightTurn: a turn that has an assistant reply but
// no EvTurnEnd yet still shows up, so live views never under-report.
func TestSnapshotIncludesInFlightTurn(t *testing.T) {
	tr := NewTracker()
	tr.Observe(agent.EvTurnStart{Turn: 1})
	tr.Observe(assistantEnd("anthropic", "claude-sonnet-4-6", usage(50, 5, 0.005)))
	snap := tr.Snapshot()
	if len(snap.Turns) != 1 || snap.Total.Input != 50 {
		t.Fatalf("snapshot = %+v, want the in-flight turn", snap)
	}
	// A bare turn with no reply yet must not appear.
	tr.Observe(agent.EvTurnEnd{Turn: 1})
	tr.Observe(agent.EvTurnStart{Turn: 2})
	if got := len(tr.Snapshot().Turns); got != 1 {
		t.Fatalf("turns = %d, want 1 (empty in-flight turn hidden)", got)
	}
}

// TestTotalsMatchesSnapshot: the cheap accessor reports the same figures as
// the full snapshot (the status bar uses it on every assistant message).
func TestTotalsMatchesSnapshot(t *testing.T) {
	tr := NewTracker()
	tr.Observe(agent.EvTurnStart{Turn: 1})
	tr.Observe(assistantEnd("anthropic", "claude-sonnet-4-6", usage(40, 4, 0.004)))
	total, lastCtx := tr.Totals()
	snap := tr.Snapshot()
	if total != snap.Total || lastCtx != snap.LastContext {
		t.Fatalf("Totals() = (%+v, %d), Snapshot = (%+v, %d)", total, lastCtx, snap.Total, snap.LastContext)
	}
}

// TestRecordTurnSink: the UsageSink path aggregates identically to events.
func TestRecordTurnSink(t *testing.T) {
	tr := NewTracker()
	tr.RecordTurn(1, "anthropic/claude-sonnet-4-6", usage(10, 1, 0.001))
	tr.RecordTurn(2, "anthropic/claude-sonnet-4-6", usage(20, 2, 0.002))
	snap := tr.Snapshot()
	if len(snap.Turns) != 2 || snap.Total.Input != 30 || snap.ByModel["anthropic/claude-sonnet-4-6"].Output != 3 {
		t.Fatalf("snapshot = %+v", snap)
	}
	if snap.LastContext != 22 {
		t.Errorf("lastContext = %d, want 22", snap.LastContext)
	}
}

// TestErrorTurnWithoutUsageIgnored: failed turns persist an assistant
// message with zero usage; they must not pollute the aggregates.
func TestErrorTurnWithoutUsageIgnored(t *testing.T) {
	tr := NewTracker()
	tr.Observe(agent.EvTurnStart{Turn: 1})
	tr.Observe(assistantEnd("anthropic", "claude-sonnet-4-6", ai.Usage{}))
	tr.Observe(agent.EvTurnEnd{Turn: 1})
	snap := tr.Snapshot()
	if len(snap.Turns) != 1 || snap.Turns[0].Model != "" {
		t.Fatalf("turns = %+v, want one usage-less record", snap.Turns)
	}
	if snap.Total != (ai.Usage{}) || snap.LastContext != 0 {
		t.Errorf("aggregates moved: %+v", snap)
	}
}
