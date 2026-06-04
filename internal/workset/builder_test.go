package workset

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/user/mixi-agent/internal/agent"
	"github.com/user/mixi-agent/internal/ai"
)

func filler(n int) string { return strings.Repeat("x", n) }

// estimateOne mirrors the compaction package's chars/4 heuristic closely
// enough for budget assertions (test messages carry no usage anchors).
func estimateOne(m agent.AgentMessage) int {
	chars := 0
	switch v := m.(type) {
	case agent.ModelMessage:
		for _, msg := range agent.ToLLM([]agent.AgentMessage{v}) {
			switch mm := msg.(type) {
			case ai.UserMessage:
				chars += contentLen(mm.Content)
			case ai.AssistantMessage:
				chars += contentLen(mm.Content)
			case ai.ToolResultMessage:
				chars += contentLen(mm.Content)
			}
		}
	case agent.CompactionSummary:
		chars = len(v.Summary)
	case agent.CustomMessage:
		chars = len(v.Content)
	}
	return (chars + 3) / 4
}

func contentLen(cs []ai.Content) int {
	n := 0
	for _, c := range cs {
		if t, ok := c.(ai.TextContent); ok {
			n += len(t.Text)
		}
	}
	return n
}

func estimateAll(msgs []agent.AgentMessage) int {
	total := 0
	for _, m := range msgs {
		total += estimateOne(m)
	}
	return total
}

// newTestBuilder wires the injected estimators every test needs.
func newTestBuilder(window, reserve int) *Builder {
	return &Builder{Window: window, Reserve: reserve, Estimate: estimateAll, EstimateOne: estimateOne}
}

func user(text string) agent.AgentMessage {
	return agent.ModelMessage{Msg: ai.UserMessage{Content: []ai.Content{ai.TextContent{Text: text}}}}
}

func assistant(text string, usage int) agent.AgentMessage {
	return agent.ModelMessage{Msg: ai.AssistantMessage{
		Content: []ai.Content{ai.TextContent{Text: text}},
		Usage:   ai.Usage{Total: usage},
	}}
}

func toolResult(id, text string) agent.AgentMessage {
	return agent.ModelMessage{Msg: ai.ToolResultMessage{
		ToolCallID: id, Content: []ai.Content{ai.TextContent{Text: text}},
	}}
}

func textOf(t *testing.T, m agent.AgentMessage) string {
	t.Helper()
	msgs := agent.ToLLM([]agent.AgentMessage{m})
	if len(msgs) == 0 {
		return ""
	}
	var parts []string
	switch v := msgs[0].(type) {
	case ai.UserMessage:
		for _, c := range v.Content {
			if tc, ok := c.(ai.TextContent); ok {
				parts = append(parts, tc.Text)
			}
		}
	case ai.AssistantMessage:
		for _, c := range v.Content {
			if tc, ok := c.(ai.TextContent); ok {
				parts = append(parts, tc.Text)
			}
		}
	case ai.ToolResultMessage:
		for _, c := range v.Content {
			if tc, ok := c.(ai.TextContent); ok {
				parts = append(parts, tc.Text)
			}
		}
	}
	return strings.Join(parts, "\n")
}

func TestBuildAssemblyOrderAfterCompaction(t *testing.T) {
	msgs := []agent.AgentMessage{
		user("old question"),
		assistant("old answer", 0),
		user("recent question"),
		assistant("recent answer", 0),
	}
	b := newTestBuilder(200_000, 0)
	b.SetCompactionState("what happened before", 2, []agent.AgentMessage{user("pinned note")})

	out, err := b.Build(context.Background(), msgs)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 4 {
		t.Fatalf("got %d messages, want 4 (summary, pinned, 2 kept)", len(out))
	}
	if _, ok := out[0].(agent.CompactionSummary); !ok {
		t.Fatalf("out[0] is %T, want CompactionSummary", out[0])
	}
	if got := textOf(t, out[1]); got != "pinned note" {
		t.Fatalf("out[1] = %q, want pinned note after summary", got)
	}
	if got := textOf(t, out[2]); got != "recent question" {
		t.Fatalf("out[2] = %q, want first kept message", got)
	}
}

func TestBuildWithoutCompactionPassesThrough(t *testing.T) {
	msgs := []agent.AgentMessage{user("q"), assistant("a", 0)}
	b := newTestBuilder(200_000, 0)
	out, err := b.Build(context.Background(), msgs)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 {
		t.Fatalf("got %d messages, want 2", len(out))
	}
}

func TestBuildAppendsStalenessNoticeAndReReadClears(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "main.go")
	if err := os.WriteFile(p, []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws := New()
	ws.SetTurn(4)
	ws.ObserveRead(p, []byte("v1"))
	if err := os.WriteFile(p, []byte("v2 changed"), 0o644); err != nil {
		t.Fatal(err)
	}

	b := newTestBuilder(200_000, 0)
	b.WS = ws
	out, err := b.Build(context.Background(), []agent.AgentMessage{user("q")})
	if err != nil {
		t.Fatal(err)
	}
	last := textOf(t, out[len(out)-1])
	want := fmt.Sprintf("%s (read turn 4, modified)", p)
	if !strings.Contains(last, want) || !strings.HasPrefix(last, "[system] Files changed on disk") {
		t.Fatalf("staleness notice = %q, want mention of %q", last, want)
	}

	ws.ObserveRead(p, []byte("v2 changed"))
	out, err = b.Build(context.Background(), []agent.AgentMessage{user("q")})
	if err != nil {
		t.Fatal(err)
	}
	if got := textOf(t, out[len(out)-1]); strings.Contains(got, "[system] Files changed") {
		t.Fatal("staleness notice still present after re-read")
	}
}

func TestStalenessNoticeCapped(t *testing.T) {
	dir := t.TempDir()
	ws := New()
	for i := 0; i < 25; i++ {
		p := filepath.Join(dir, fmt.Sprintf("f%02d.go", i))
		if err := os.WriteFile(p, []byte("v1"), 0o644); err != nil {
			t.Fatal(err)
		}
		ws.ObserveRead(p, []byte("v1"))
		if err := os.Remove(p); err != nil {
			t.Fatal(err)
		}
	}
	b := newTestBuilder(200_000, 0)
	b.WS = ws
	out, err := b.Build(context.Background(), []agent.AgentMessage{user("q")})
	if err != nil {
		t.Fatal(err)
	}
	notice := textOf(t, out[len(out)-1])
	if !strings.Contains(notice, ", and 5 more") {
		t.Fatalf("notice not capped: %q", notice)
	}
	if got := strings.Count(notice, "(deleted)"); got != 20 {
		t.Fatalf("notice lists %d files, want 20", got)
	}
}

func TestTrimOldFatToolResultsProjectionOnly(t *testing.T) {
	fat := filler(5000)
	msgs := []agent.AgentMessage{
		user("q1"),
		assistant("a1", 0),
		toolResult("t1", fat), // old: trimmable
		assistant("a2", 0),    // protected from here (2nd-from-last assistant)
		toolResult("t2", fat), // recent: protected
		assistant("a3", 0),
	}
	// Estimate ≈ (2+2+5000+2+5000+2)/4 ≈ 2502. Budget = window−reserve(100)−0.
	// Window 2200 → budget 2100 → one trim (~1250 saved) gets under.
	b := newTestBuilder(2200, 100)
	out, err := b.Build(context.Background(), msgs)
	if err != nil {
		t.Fatal(err)
	}
	if got := textOf(t, out[2]); !strings.Contains(got, "[tool result trimmed after context pressure; originally 5000 bytes") {
		t.Fatalf("old fat result not trimmed: %q", got[:60])
	}
	if got := textOf(t, out[4]); got != fat {
		t.Fatal("protected recent tool result was trimmed")
	}
	// Projection-only: the input slice still holds the original result.
	if got := textOf(t, msgs[2]); got != fat {
		t.Fatal("input messages mutated by trim")
	}
}

func TestBuildCompactsWhenTrimInsufficient(t *testing.T) {
	fat := filler(4000)
	msgs := []agent.AgentMessage{
		user(fat), user(fat), user(fat), user(fat),
		user("recent"),
	}
	b := newTestBuilder(1100, 100) // budget 1000, estimate ~4000
	compacted := false
	b.CompactNow = func(ctx context.Context) error {
		compacted = true
		b.SetCompactionState("squashed", 4, nil)
		return nil
	}
	out, err := b.Build(context.Background(), msgs)
	if err != nil {
		t.Fatal(err)
	}
	if !compacted {
		t.Fatal("compaction stage not triggered")
	}
	if _, ok := out[0].(agent.CompactionSummary); !ok || len(out) != 2 {
		t.Fatalf("projection after compaction wrong: %d messages, first %T", len(out), out[0])
	}
}

func TestBuildHardErrorWhenNothingHelps(t *testing.T) {
	msgs := []agent.AgentMessage{user(filler(40_000))} // single giant turn
	b := newTestBuilder(1100, 100)
	b.CompactNow = func(ctx context.Context) error { return errors.New("nothing to compact") }
	if _, err := b.Build(context.Background(), msgs); err == nil ||
		!strings.Contains(err.Error(), "exceeds the request budget") {
		t.Fatalf("err = %v, want hard budget error", err)
	}
}
