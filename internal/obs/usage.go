package obs

import (
	"fmt"
	"sync"
	"time"

	"github.com/user/mixi-agent/internal/agent"
	"github.com/user/mixi-agent/internal/ai"
)

// UsageSink receives per-turn usage. The live Tracker implements it; replay
// aggregation and RPC stat ingestion are future implementations.
type UsageSink interface {
	RecordTurn(turn int, model string, u ai.Usage)
}

// TurnUsage is one turn's usage record.
type TurnUsage struct {
	Turn      int
	Model     string // provider/id
	Usage     ai.Usage
	DurMs     int64
	ToolCalls int
}

// Snapshot is a point-in-time copy of a tracker's aggregates.
type Snapshot struct {
	Turns   []TurnUsage
	Total   ai.Usage
	ByModel map[string]ai.Usage
	// LastContext is the token count occupying the context window after the
	// latest assistant reply (input + cache + output).
	LastContext int
}

// Summary is the one-line figure shared by /cost and --print-stats so every
// surface always reports identical numbers.
func (s Snapshot) Summary() string {
	return fmt.Sprintf("turns=%d tokens: input=%d output=%d cacheRead=%d cacheWrite=%d total=%d cost=$%.4f",
		len(s.Turns), s.Total.Input, s.Total.Output, s.Total.CacheRead,
		s.Total.CacheWrite, s.Total.Total, s.Total.Cost.Total)
}

// Tracker folds agent events into usage aggregates. Safe for concurrent
// Observe/Snapshot (the TUI update loop reads while RPC consumers poll).
type Tracker struct {
	mu      sync.Mutex
	turns   []TurnUsage
	cur     *TurnUsage
	curFrom time.Time
	total   ai.Usage
	byModel map[string]ai.Usage
	lastCtx int
	now     func() time.Time // injectable clock for duration tests
}

func NewTracker() *Tracker {
	return &Tracker{byModel: map[string]ai.Usage{}, now: time.Now}
}

// Observe folds one agent event into the aggregates; feed it every event of
// a run. Only assistant messages carry usage, so steering/user messages and
// render-only deltas fall through untouched.
func (t *Tracker) Observe(ev agent.Event) {
	t.mu.Lock()
	defer t.mu.Unlock()
	switch e := ev.(type) {
	case agent.EvTurnStart:
		t.cur = &TurnUsage{Turn: e.Turn}
		t.curFrom = t.now()
	case agent.EvToolStart:
		if t.cur != nil {
			t.cur.ToolCalls++
		}
	case agent.EvMessageEnd:
		am, ok := assistantOf(e.Msg)
		if !ok || am.Usage.Total == 0 {
			return
		}
		key := am.Provider + "/" + am.Model
		if t.cur != nil {
			t.cur.Model = key
			addUsage(&t.cur.Usage, am.Usage)
		}
		addUsage(&t.total, am.Usage)
		u := t.byModel[key]
		addUsage(&u, am.Usage)
		t.byModel[key] = u
		t.lastCtx = am.Usage.Input + am.Usage.CacheRead + am.Usage.CacheWrite + am.Usage.Output
	case agent.EvTurnEnd:
		if t.cur == nil {
			return
		}
		t.cur.DurMs = t.now().Sub(t.curFrom).Milliseconds()
		t.turns = append(t.turns, *t.cur)
		t.cur = nil
	}
}

// RecordTurn implements UsageSink for producers that aggregate recorded
// usage directly rather than speaking agent events.
func (t *Tracker) RecordTurn(turn int, model string, u ai.Usage) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.turns = append(t.turns, TurnUsage{Turn: turn, Model: model, Usage: u})
	addUsage(&t.total, u)
	mu := t.byModel[model]
	addUsage(&mu, u)
	t.byModel[model] = mu
	t.lastCtx = u.Input + u.CacheRead + u.CacheWrite + u.Output
}

// Totals returns the running session usage and last context size without
// copying the per-turn records — cheap enough for per-event UI refreshes.
func (t *Tracker) Totals() (total ai.Usage, lastContext int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.total, t.lastCtx
}

// Snapshot copies the aggregates. An in-flight turn that already saw an
// assistant reply is included so live consumers never under-report.
func (t *Tracker) Snapshot() Snapshot {
	t.mu.Lock()
	defer t.mu.Unlock()
	turns := append([]TurnUsage(nil), t.turns...)
	if t.cur != nil && t.cur.Model != "" {
		turns = append(turns, *t.cur)
	}
	byModel := make(map[string]ai.Usage, len(t.byModel))
	for k, v := range t.byModel {
		byModel[k] = v
	}
	return Snapshot{Turns: turns, Total: t.total, ByModel: byModel, LastContext: t.lastCtx}
}

// assistantOf unwraps an agent message down to its assistant payload.
func assistantOf(m agent.AgentMessage) (ai.AssistantMessage, bool) {
	mm, ok := m.(agent.ModelMessage)
	if !ok {
		return ai.AssistantMessage{}, false
	}
	am, ok := mm.Msg.(ai.AssistantMessage)
	return am, ok
}

func addUsage(dst *ai.Usage, u ai.Usage) {
	dst.Input += u.Input
	dst.Output += u.Output
	dst.CacheRead += u.CacheRead
	dst.CacheWrite += u.CacheWrite
	dst.Total += u.Total
	dst.Cost.Input += u.Cost.Input
	dst.Cost.Output += u.Cost.Output
	dst.Cost.CacheRead += u.Cost.CacheRead
	dst.Cost.CacheWrite += u.Cost.CacheWrite
	dst.Cost.Total += u.Cost.Total
}
