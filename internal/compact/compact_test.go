package compact

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/user/mixi-agent/internal/ai"
	"github.com/user/mixi-agent/internal/session"
)

// fixture builders — IDs are assigned manually so expectations are stable.

func userEntry(id string, chars int) session.Entry {
	return msgEntry(id, ai.UserMessage{Content: []ai.Content{ai.TextContent{Text: text(chars)}}})
}

func assistantEntry(id string, chars, usageTotal int) session.Entry {
	return msgEntry(id, ai.AssistantMessage{
		Content: []ai.Content{ai.TextContent{Text: text(chars)}},
		Usage:   ai.Usage{Total: usageTotal},
	})
}

func toolResultEntry(id string, chars int) session.Entry {
	return msgEntry(id, ai.ToolResultMessage{Content: []ai.Content{ai.TextContent{Text: text(chars)}}})
}

// fakeSummarizer records calls and replays scripted results.
type fakeSummarizer struct {
	calls []struct{ Conversation, Prompt string }
	out   []string
	err   error
}

func (f *fakeSummarizer) Summarize(_ context.Context, conversation, prompt string, _ int) (string, error) {
	f.calls = append(f.calls, struct{ Conversation, Prompt string }{conversation, prompt})
	if f.err != nil {
		return "", f.err
	}
	i := len(f.calls) - 1
	if i < len(f.out) {
		return f.out[i], nil
	}
	return fmt.Sprintf("summary-%d", i), nil
}

func TestPlanCutCleanAtUserMessage(t *testing.T) {
	path := []session.Entry{
		userEntry("e1", 400),
		assistantEntry("e2", 400, 0),
		userEntry("e3", 400), // walk: e3 alone reaches 100 tokens
	}
	plan, err := planCut(path, 100)
	if err != nil {
		t.Fatal(err)
	}
	if got := path[plan.cutIdx].EntryID(); got != "e3" {
		t.Fatalf("cut at %s, want e3", got)
	}
	if plan.splitTurn {
		t.Fatal("clean user-message cut must not split the turn")
	}
	if plan.windowStart != 0 {
		t.Fatalf("windowStart = %d, want 0", plan.windowStart)
	}
}

func TestPlanCutSkipsToolResult(t *testing.T) {
	path := []session.Entry{
		userEntry("e1", 4000),
		assistantEntry("e2", 200, 0),
		toolResultEntry("e3", 400), // accumulation lands here: invalid cut
		assistantEntry("e4", 40, 0),
	}
	// e4=10 + e3=100 crosses 105 at the tool result → cut walks back to e2.
	plan, err := planCut(path, 105)
	if err != nil {
		t.Fatal(err)
	}
	if got := path[plan.cutIdx].EntryID(); got != "e2" {
		t.Fatalf("cut at %s, want e2 (assistant before tool result)", got)
	}
	if plan.splitTurn {
		t.Fatal("turn opening at window start must not be treated as split")
	}
}

func TestPlanCutSplitTurn(t *testing.T) {
	path := []session.Entry{
		userEntry("e1", 400),
		assistantEntry("e2", 400, 0),
		userEntry("e3", 40), // opens the turn that gets split
		assistantEntry("e4", 200, 0),
		toolResultEntry("e5", 200),
		assistantEntry("e6", 400, 0), // cut lands here, mid-turn
	}
	plan, err := planCut(path, 100)
	if err != nil {
		t.Fatal(err)
	}
	if got := path[plan.cutIdx].EntryID(); got != "e6" {
		t.Fatalf("cut at %s, want e6", got)
	}
	if !plan.splitTurn {
		t.Fatal("expected a split turn")
	}
	if got := path[plan.turnStartIdx].EntryID(); got != "e3" {
		t.Fatalf("turn start at %s, want e3", got)
	}
}

func TestPlanCutRepeatCompactionBoundary(t *testing.T) {
	prev := &session.CompactionEntry{Summary: "old summary", FirstKeptEntryID: "e3",
		Details: session.FileLists{ReadFiles: []string{"a.go"}}}
	prev.ID = "e5"
	path := []session.Entry{
		userEntry("e1", 400),
		assistantEntry("e2", 400, 0),
		userEntry("e3", 400), // previous compaction kept from here
		assistantEntry("e4", 400, 0),
		prev,
		userEntry("e6", 40),
		assistantEntry("e7", 400, 0),
	}
	plan, err := planCut(path, 100)
	if err != nil {
		t.Fatal(err)
	}
	if plan.windowStart != 2 {
		t.Fatalf("windowStart = %d, want 2 (previous firstKeptEntryId)", plan.windowStart)
	}
	if plan.prevSummary != "old summary" {
		t.Fatalf("prevSummary = %q", plan.prevSummary)
	}
	if got := path[plan.cutIdx].EntryID(); got != "e7" {
		t.Fatalf("cut at %s, want e7", got)
	}
	if !plan.splitTurn || path[plan.turnStartIdx].EntryID() != "e6" {
		t.Fatalf("expected split at turn start e6, got split=%v start=%d", plan.splitTurn, plan.turnStartIdx)
	}
}

func TestPlanCutExtendsOverSettingsEntries(t *testing.T) {
	mc := &session.ModelChangeEntry{Provider: "anthropic", ModelID: "m"}
	mc.ID = "e3"
	path := []session.Entry{
		userEntry("e1", 400),
		assistantEntry("e2", 400, 0),
		mc,                   // rides along with the kept tail
		userEntry("e4", 400), // candidate cut
	}
	plan, err := planCut(path, 100)
	if err != nil {
		t.Fatal(err)
	}
	if got := path[plan.cutIdx].EntryID(); got != "e3" {
		t.Fatalf("cut at %s, want e3 (settings entry kept with following message)", got)
	}
}

func TestPlanCutNothingToCompact(t *testing.T) {
	path := []session.Entry{
		userEntry("e1", 40),
		assistantEntry("e2", 40, 0),
	}
	if _, err := planCut(path, 20000); !errors.Is(err, ErrNothingToCompact) {
		t.Fatalf("err = %v, want ErrNothingToCompact", err)
	}
}

// storeWith appends fixture messages to a fresh disk store; IDs are assigned
// by the store, so assertions use positions via PathToRoot.
func storeWith(t *testing.T, n int) (session.Storage, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "s.jsonl")
	store, err := session.NewJSONL(path, session.Header{CWD: dir}, session.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	for i := 0; i < n; i++ {
		var m ai.Message
		if i%2 == 0 {
			m = ai.UserMessage{Content: []ai.Content{ai.TextContent{Text: text(400)}}}
		} else {
			m = ai.AssistantMessage{Content: []ai.Content{ai.TextContent{Text: text(400)}}, Usage: ai.Usage{Total: 50000}}
		}
		if err := store.Append(&session.MessageEntry{Message: m}); err != nil {
			t.Fatal(err)
		}
	}
	return store, path
}

func TestCompactAppendsEntry(t *testing.T) {
	store, _ := storeWith(t, 8)
	fake := &fakeSummarizer{out: []string{"the summary"}}
	c := &Compactor{Summarizer: fake, KeepRecent: 100, Reserve: 1000}

	got, err := c.Compact(context.Background(), store, Request{
		Read:    []string{"r.go", "m.go"},
		Written: []string{"m.go"},
	})
	if err != nil {
		t.Fatal(err)
	}
	pathEntries := store.PathToRoot(store.LeafID())
	last := pathEntries[len(pathEntries)-1]
	ce, ok := last.(*session.CompactionEntry)
	if !ok {
		t.Fatalf("last path entry is %T, want CompactionEntry", last)
	}
	if ce.FirstKeptEntryID != got.FirstKeptEntryID || ce.FirstKeptEntryID == "" {
		t.Fatalf("firstKeptEntryId mismatch: %q vs %q", ce.FirstKeptEntryID, got.FirstKeptEntryID)
	}
	if !strings.HasPrefix(ce.Summary, "the summary") {
		t.Fatalf("summary = %q", ce.Summary)
	}
	if ce.TokensBefore == 0 {
		t.Fatal("tokensBefore not recorded")
	}
	wantRead := []string{"r.go"}
	if len(ce.Details.ReadFiles) != 1 || ce.Details.ReadFiles[0] != wantRead[0] {
		t.Fatalf("readFiles = %v, want %v (modified wins)", ce.Details.ReadFiles, wantRead)
	}
	if len(ce.Details.ModifiedFiles) != 1 || ce.Details.ModifiedFiles[0] != "m.go" {
		t.Fatalf("modifiedFiles = %v", ce.Details.ModifiedFiles)
	}
	if !strings.Contains(fake.calls[0].Prompt, "## Goal") || strings.Contains(fake.calls[0].Conversation, "<previous-summary>") {
		t.Fatal("first compaction must use the initial summarization prompt without previous summary")
	}
}

func TestCompactSplitTurnMergesPrefixSummary(t *testing.T) {
	store, _ := storeWith(t, 8) // even count: path ends with an assistant message
	fake := &fakeSummarizer{out: []string{"history", "prefix"}}
	// keepRecent=100 puts the cut on the trailing assistant message — mid-turn.
	c := &Compactor{Summarizer: fake, KeepRecent: 100, Reserve: 1000}

	got, err := c.Compact(context.Background(), store, Request{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Summary != "history\n---\nprefix" {
		t.Fatalf("summary = %q, want merged history---prefix", got.Summary)
	}
	if len(fake.calls) != 2 || !strings.Contains(fake.calls[1].Prompt, "first part of an in-progress") {
		t.Fatalf("expected turn-prefix second call, got %d calls", len(fake.calls))
	}
}

func TestCompactFailureLeavesSessionByteIdentical(t *testing.T) {
	store, path := storeWith(t, 8)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	c := &Compactor{Summarizer: &fakeSummarizer{err: errors.New("llm down")}, KeepRecent: 100}
	if _, err := c.Compact(context.Background(), store, Request{}); err == nil {
		t.Fatal("expected error from failing summarizer")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("session file changed despite compaction failure")
	}
}

func TestCompactRepeatUsesUpdatePrompt(t *testing.T) {
	store, _ := storeWith(t, 8)
	fake := &fakeSummarizer{out: []string{"first summary"}}
	c := &Compactor{Summarizer: fake, KeepRecent: 100, Reserve: 1000}
	if _, err := c.Compact(context.Background(), store, Request{}); err != nil {
		t.Fatal(err)
	}
	// Grow the tail past keepRecent again.
	for i := 0; i < 6; i++ {
		var m ai.Message
		if i%2 == 0 {
			m = ai.UserMessage{Content: []ai.Content{ai.TextContent{Text: text(400)}}}
		} else {
			m = ai.AssistantMessage{Content: []ai.Content{ai.TextContent{Text: text(400)}}}
		}
		if err := store.Append(&session.MessageEntry{Message: m}); err != nil {
			t.Fatal(err)
		}
	}
	fake2 := &fakeSummarizer{out: []string{"second"}}
	c.Summarizer = fake2
	if _, err := c.Compact(context.Background(), store, Request{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fake2.calls[0].Conversation, "<previous-summary>\nfirst summary") {
		t.Fatal("update compaction must carry the previous summary")
	}
	if !strings.Contains(fake2.calls[0].Prompt, "updating an existing summary") {
		t.Fatal("update compaction must use the update prompt")
	}
}

func TestCompactManualFocusAndPinnedNote(t *testing.T) {
	store, _ := storeWith(t, 6)
	pinned := &session.MessageEntry{
		Message: ai.UserMessage{Content: []ai.Content{ai.TextContent{Text: text(400)}}},
		Pinned:  true,
	}
	if err := store.Append(pinned); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		if err := store.Append(&session.MessageEntry{
			Message: ai.UserMessage{Content: []ai.Content{ai.TextContent{Text: text(400)}}},
		}); err != nil {
			t.Fatal(err)
		}
	}
	fake := &fakeSummarizer{}
	c := &Compactor{Summarizer: fake, KeepRecent: 100, Reserve: 1000}
	if _, err := c.Compact(context.Background(), store, Request{Focus: "keep auth details"}); err != nil {
		t.Fatal(err)
	}
	p := fake.calls[0].Prompt
	if !strings.Contains(p, "Additional focus: keep auth details") {
		t.Fatal("manual focus missing from prompt")
	}
	if !strings.Contains(p, pinnedExclusionNote) {
		t.Fatal("pinned-exclusion note missing despite pinned entry before cut")
	}
}

func TestMergeFileLists(t *testing.T) {
	prev := session.FileLists{ReadFiles: []string{"a.go", "b.go"}, ModifiedFiles: []string{"c.go"}}
	got := MergeFileLists(prev, []string{"b.go", "d.go"}, []string{"b.go", "e.go"})
	wantRead := "a.go,d.go"
	wantMod := "b.go,c.go,e.go"
	if strings.Join(got.ReadFiles, ",") != wantRead {
		t.Fatalf("readFiles = %v, want %s", got.ReadFiles, wantRead)
	}
	if strings.Join(got.ModifiedFiles, ",") != wantMod {
		t.Fatalf("modifiedFiles = %v, want %s", got.ModifiedFiles, wantMod)
	}
}
