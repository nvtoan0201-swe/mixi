package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/user/mixi-agent/internal/ai"
)

func TestMain(m *testing.M) {
	// Deterministic plain-text rendering for golden assertions.
	lipgloss.SetColorProfile(termenv.Ascii)
	m.Run()
}

func plainCtx(expandThinking, expandTools bool) renderCtx {
	return renderCtx{
		width:          80,
		expandThinking: expandThinking,
		expandTools:    expandTools,
		spinner:        "*",
		markdown:       func(s string) string { return s },
	}
}

func TestMsgBlockStreamingThenDone(t *testing.T) {
	b := &msgBlock{blockID: "m1", role: ai.RoleAssistant}
	b.appendDelta(segThinking, "step one\nstep two\nstep three")
	b.appendDelta(segText, "Hello **world**")

	got := b.render(plainCtx(false, false))
	if !strings.Contains(got, "step one (+2 lines, ctrl+t)") {
		t.Errorf("collapsed thinking missing fold marker:\n%s", got)
	}
	if !strings.Contains(got, "Hello **world**") {
		t.Errorf("streaming text should be raw:\n%s", got)
	}

	got = b.render(plainCtx(true, false))
	if !strings.Contains(got, "step three") {
		t.Errorf("expanded thinking should show all lines:\n%s", got)
	}

	b.done = true
	rendered := 0
	rc := plainCtx(false, false)
	rc.markdown = func(s string) string { rendered++; return "MD:" + s }
	first := b.render(rc)
	second := b.render(rc)
	if !strings.Contains(first, "MD:Hello") {
		t.Errorf("finished message must go through markdown:\n%s", first)
	}
	if first != second || rendered != 1 {
		t.Errorf("finished render must be cached (rendered=%d)", rendered)
	}
}

func TestMsgBlockUserPrefix(t *testing.T) {
	b := &msgBlock{blockID: "u1", role: ai.RoleUser, done: true}
	b.appendDelta(segText, "fix the bug")
	if got := b.render(plainCtx(false, false)); !strings.Contains(got, "> fix the bug") {
		t.Errorf("user message missing prompt prefix:\n%s", got)
	}
}

func TestToolBlockStates(t *testing.T) {
	tb := &toolBlock{call: ai.ToolCall{ID: "t1", Name: "bash",
		Args: []byte(`{"command":"go test ./..."}`)}, state: toolRunning}
	got := tb.render(plainCtx(false, false))
	if !strings.Contains(got, "*") || !strings.Contains(got, "⚙ bash go test ./...") {
		t.Errorf("running card needs spinner and header:\n%s", got)
	}

	tb.finish(ai.ToolResultMessage{ToolCallID: "t1", ToolName: "bash",
		Content: []ai.Content{ai.TextContent{Text: "line1\nline2"}}})
	got = tb.render(plainCtx(false, false))
	if !strings.Contains(got, "✓") || !strings.Contains(got, "line1") {
		t.Errorf("done card needs check mark and body:\n%s", got)
	}

	tb.finish(ai.ToolResultMessage{IsError: true,
		Content: []ai.Content{ai.TextContent{Text: "boom"}}})
	if got = tb.render(plainCtx(false, false)); !strings.Contains(got, "✗") {
		t.Errorf("failed card needs cross mark:\n%s", got)
	}
}

func TestToolBlockFoldAndExpand(t *testing.T) {
	long := strings.Repeat("x\n", 20)
	tb := &toolBlock{call: ai.ToolCall{ID: "t1", Name: "read", Args: []byte(`{"path":"a.txt"}`)}}
	tb.finish(ai.ToolResultMessage{Content: []ai.Content{ai.TextContent{Text: long}}})

	folded := tb.render(plainCtx(false, false))
	if !strings.Contains(folded, "(+12 lines, ctrl+o)") {
		t.Errorf("folded body must report hidden lines:\n%s", folded)
	}
	expanded := tb.render(plainCtx(false, true))
	if strings.Contains(expanded, "ctrl+o)") {
		t.Errorf("expanded body must not fold:\n%s", expanded)
	}
}

func TestToolBlockDiffFromDetails(t *testing.T) {
	tb := &toolBlock{call: ai.ToolCall{ID: "t1", Name: "edit", Args: []byte(`{"path":"a.go"}`)}}
	tb.finish(ai.ToolResultMessage{
		Content: []ai.Content{ai.TextContent{Text: "edited a.go"}},
		Details: []byte(`{"diff":"@@ -1,1 +1,1 @@\n-old\n+new"}`),
	})
	got := tb.render(plainCtx(false, true))
	for _, want := range []string{"@@ -1,1 +1,1 @@", "-old", "+new"} {
		if !strings.Contains(got, want) {
			t.Errorf("edit card must render the diff (%q missing):\n%s", want, got)
		}
	}
}

func TestStatusBarFields(t *testing.T) {
	s := statusModel{
		model:    ai.Model{DisplayName: "Test Model", ContextWindow: 100_000},
		thinking: ai.ThinkingMedium, permMode: "prompt",
		cost: 0.0208, ctxTokens: 41_000, jobs: 2, width: 80,
	}
	got := s.view()
	for _, want := range []string{"Test Model", "think:medium", "ctx 41%", "$0.0208", "mode:prompt", "2 jobs"} {
		if !strings.Contains(got, want) {
			t.Errorf("status bar missing %q:\n%s", want, got)
		}
	}
}

func TestEditorHistoryRing(t *testing.T) {
	e := newEditor()
	e.remember("first")
	e.remember("second")

	if !e.histPrev() || e.ta.Value() != "second" {
		t.Fatalf("histPrev = %q, want second", e.ta.Value())
	}
	if !e.histPrev() || e.ta.Value() != "first" {
		t.Fatalf("histPrev = %q, want first", e.ta.Value())
	}
	if e.histPrev() {
		t.Fatal("histPrev past the oldest entry must refuse")
	}
	if !e.histNext() || e.ta.Value() != "second" {
		t.Fatalf("histNext = %q, want second", e.ta.Value())
	}
	if !e.histNext() || e.ta.Value() != "" {
		t.Fatalf("histNext back to draft = %q, want empty", e.ta.Value())
	}

	for i := 0; i < historyCap+10; i++ {
		e.remember("x")
	}
	if len(e.history) != historyCap {
		t.Fatalf("history len = %d, want cap %d", len(e.history), historyCap)
	}
}

func TestSlashSuggestionsAndComplete(t *testing.T) {
	e := newEditor()
	table := commandTable()

	e.ta.SetValue("/mo")
	sugg := e.suggestions(table)
	if len(sugg) != 2 { // /mode, /model
		t.Fatalf("suggestions for /mo = %d, want 2", len(sugg))
	}
	if !e.complete(table) || e.ta.Value() != "/mode " {
		t.Fatalf("complete = %q, want \"/mode \"", e.ta.Value())
	}

	e.ta.SetValue("not a command")
	if s := e.suggestions(table); s != nil {
		t.Fatalf("plain text must not suggest, got %v", s)
	}
}
