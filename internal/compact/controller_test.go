package compact

import (
	"context"
	"strings"
	"testing"

	"github.com/user/mixi-agent/internal/agent"
	"github.com/user/mixi-agent/internal/ai"
	"github.com/user/mixi-agent/internal/session"
	"github.com/user/mixi-agent/internal/workset"
)

func testModel() ai.Model {
	return ai.Model{API: "test", Provider: "test", ID: "m", ContextWindow: 200_000, MaxOutput: 8192}
}

func newTestController(t *testing.T, fake Summarizer, opts ...func(*ControllerConfig)) (*Controller, session.Storage) {
	t.Helper()
	m := session.Manager{Root: t.TempDir()}
	store := m.InMemory("/tmp/proj")
	cfg := ControllerConfig{
		Store:      store,
		WS:         workset.New(),
		Summarizer: fake,
		Model:      testModel(),
		Enabled:    true,
	}
	for _, o := range opts {
		o(&cfg)
	}
	return NewController(cfg), store
}

func TestOnMessagePersistsModelMessages(t *testing.T) {
	c, store := newTestController(t, &fakeSummarizer{})
	c.OnMessage(userMsgA("hello"))
	c.OnMessage(assistantMsgA("hi", 100))

	entries := store.Entries()
	if len(entries) != 2 {
		t.Fatalf("persisted %d entries, want 2", len(entries))
	}
	if entries[0].(*session.MessageEntry).Message.MsgRole() != ai.RoleUser {
		t.Fatal("first entry should be the user message")
	}
}

func userMsgA(text string) agent.AgentMessage {
	return agent.ModelMessage{Msg: ai.UserMessage{Content: []ai.Content{ai.TextContent{Text: text}}}}
}

func assistantMsgA(text string, usage int) agent.AgentMessage {
	return agent.ModelMessage{Msg: ai.AssistantMessage{
		Content: []ai.Content{ai.TextContent{Text: text}},
		Usage:   ai.Usage{Total: usage},
	}}
}

func TestAfterTurnAutoCompactsOverThreshold(t *testing.T) {
	fake := &fakeSummarizer{out: []string{"squeezed"}}
	c, store := newTestController(t, fake, func(cfg *ControllerConfig) {
		cfg.KeepRecent = 50
	})
	// Build a conversation whose anchored estimate exceeds window − reserve.
	for i := 0; i < 4; i++ {
		c.OnMessage(userMsgA(text(400)))
		c.OnMessage(assistantMsgA(text(400), 0))
	}
	c.OnMessage(userMsgA(text(400)))
	c.OnMessage(assistantMsgA(text(400), 190_000)) // anchor over 200000−16384

	if err := c.AfterTurn(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if len(fake.calls) == 0 {
		t.Fatal("auto-compaction did not run")
	}
	last := store.Entries()[len(store.Entries())-1]
	if _, ok := last.(*session.CompactionEntry); !ok {
		t.Fatalf("last entry = %T, want CompactionEntry", last)
	}

	// The anchor now predates the compaction: a second AfterTurn with the
	// same stale estimate must NOT compact again.
	calls := len(fake.calls)
	if err := c.AfterTurn(context.Background(), 2); err != nil {
		t.Fatal(err)
	}
	if len(fake.calls) != calls {
		t.Fatal("re-compacted on a stale usage anchor")
	}
}

func TestAfterTurnRespectsKillSwitch(t *testing.T) {
	fake := &fakeSummarizer{}
	c, _ := newTestController(t, fake, func(cfg *ControllerConfig) { cfg.Enabled = false })
	c.OnMessage(assistantMsgA(text(40), 250_000))
	if err := c.AfterTurn(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if len(fake.calls) != 0 {
		t.Fatal("compaction ran despite compaction.disabled")
	}
}

func TestTransformContextProjectsAfterCompaction(t *testing.T) {
	fake := &fakeSummarizer{out: []string{"the past, summarized"}}
	c, _ := newTestController(t, fake, func(cfg *ControllerConfig) { cfg.KeepRecent = 50 })

	pinnedMsg := agent.ModelMessage{Msg: ai.UserMessage{
		Content: []ai.Content{ai.TextContent{Text: "pin me"}},
	}}
	// Persist a pinned entry the way a /pin would: flag set on the entry.
	c.OnMessage(pinnedMsg)
	c.mu.Lock()
	c.pinned[0] = true
	c.mu.Unlock()
	for i := 0; i < 6; i++ {
		c.OnMessage(userMsgA(text(400)))
		c.OnMessage(assistantMsgA(text(400), 0))
	}
	c.OnMessage(assistantMsgA(text(400), 190_000))
	if err := c.AfterTurn(context.Background(), 1); err != nil {
		t.Fatal(err)
	}

	msgs := c.mirror()
	out, err := c.TransformContext(context.Background(), msgs)
	if err != nil {
		t.Fatal(err)
	}
	sum, ok := out[0].(agent.CompactionSummary)
	if !ok {
		t.Fatalf("out[0] = %T, want CompactionSummary", out[0])
	}
	if !strings.Contains(sum.Summary, "the past, summarized") {
		t.Fatalf("summary = %q", sum.Summary)
	}
	mm, ok := out[1].(agent.ModelMessage)
	if !ok {
		t.Fatalf("out[1] = %T, want pinned message", out[1])
	}
	um := mm.Msg.(ai.UserMessage)
	if um.Content[0].(ai.TextContent).Text != "pin me" {
		t.Fatal("pinned message did not survive compaction after the summary")
	}
	if len(out) >= len(msgs) {
		t.Fatalf("projection did not shrink: %d → %d", len(msgs), len(out))
	}
}

// mirror returns a copy of the controller's conversation for test builds.
func (c *Controller) mirror() []agent.AgentMessage {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]agent.AgentMessage(nil), c.msgs...)
}

func TestOverflowRecoveryCompacts(t *testing.T) {
	fake := &fakeSummarizer{out: []string{"compact on overflow"}}
	c, store := newTestController(t, fake, func(cfg *ControllerConfig) { cfg.KeepRecent = 50 })
	for i := 0; i < 6; i++ {
		c.OnMessage(userMsgA(text(400)))
		c.OnMessage(assistantMsgA(text(400), 0))
	}
	if !c.OnContextOverflow(context.Background()) {
		t.Fatal("overflow recovery failed")
	}
	last := store.Entries()[len(store.Entries())-1]
	if _, ok := last.(*session.CompactionEntry); !ok {
		t.Fatalf("last entry = %T, want CompactionEntry", last)
	}
}

func TestOverflowRecoveryFalseWhenNothingToCompact(t *testing.T) {
	c, _ := newTestController(t, &fakeSummarizer{})
	c.OnMessage(userMsgA("tiny"))
	if c.OnContextOverflow(context.Background()) {
		t.Fatal("claimed recovery on an empty session")
	}
}

func TestLoadHistoryRestoresCompactionAndWorkset(t *testing.T) {
	m := session.Manager{Root: t.TempDir()}
	store := m.InMemory("/tmp/proj")
	ws := workset.New()

	// First life of the session: messages, then a compaction.
	fake := &fakeSummarizer{out: []string{"first life summary"}}
	c1 := NewController(ControllerConfig{
		Store: store, WS: ws, Summarizer: fake, Model: testModel(),
		Enabled: true, KeepRecent: 50,
	})
	for i := 0; i < 5; i++ {
		c1.OnMessage(userMsgA(text(400)))
		c1.OnMessage(assistantMsgA(text(400), 0))
	}
	c1.OnMessage(assistantMsgA(text(400), 190_000))
	if err := c1.AfterTurn(context.Background(), 1); err != nil {
		t.Fatal(err)
	}

	// Resume: a fresh controller over the same store.
	c2 := NewController(ControllerConfig{
		Store: store, WS: workset.New(), Summarizer: &fakeSummarizer{}, Model: testModel(),
		Enabled: true,
	})
	history := c2.LoadHistory()
	if len(history) == 0 {
		t.Fatal("no history restored")
	}
	out, err := c2.TransformContext(context.Background(), history)
	if err != nil {
		t.Fatal(err)
	}
	sum, ok := out[0].(agent.CompactionSummary)
	if !ok || !strings.Contains(sum.Summary, "first life summary") {
		t.Fatalf("resumed projection lacks compaction summary: %T", out[0])
	}
	if len(out) >= len(history) {
		t.Fatal("resumed projection did not apply the cut")
	}
}

func TestBuildPreflightCompactsAtMostOncePerTurn(t *testing.T) {
	// Summaries fatter than the budget keep the estimate over it, so Build
	// will want to compact every time; the guard must allow only one.
	fake := &fakeSummarizer{out: []string{text(800_000)}}
	c, _ := newTestController(t, fake, func(cfg *ControllerConfig) { cfg.KeepRecent = 50 })
	var msgs []agent.AgentMessage
	push := func(m agent.AgentMessage) { c.OnMessage(m); msgs = append(msgs, m) }
	for i := 0; i < 5; i++ {
		push(userMsgA(text(400)))
		push(assistantMsgA(text(400), 0))
	}
	push(assistantMsgA(text(400), 1_000_000)) // hopelessly over any budget

	if _, err := c.TransformContext(context.Background(), msgs); err == nil {
		t.Fatal("expected hard budget error for an unsalvageable context")
	}
	if got := summarizeCallCount(fake); got > 2 {
		t.Fatalf("summarizer invoked %d times; pre-flight compaction must run at most once", got)
	}
}

// summarizeCallCount counts main-summary invocations (split turns add one).
func summarizeCallCount(f *fakeSummarizer) int { return len(f.calls) }
