package modes

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/user/mixi-agent/internal/agent"
	"github.com/user/mixi-agent/internal/agent/agenttest"
	"github.com/user/mixi-agent/internal/ai"
	"github.com/user/mixi-agent/internal/compact"
	"github.com/user/mixi-agent/internal/session"
	"github.com/user/mixi-agent/internal/tools"
	"github.com/user/mixi-agent/internal/workset"
)

// e2eRun wires the full print-mode stack — disk session, working set,
// controller hooks, built-in tools — over a scripted provider and executes
// one run. The same scripted stream serves both the agent loop and the
// summarizer, exactly like a real single-model deployment.
func e2eRun(t *testing.T, cwd string, opts PrintOptions, keepRecent int, turns ...agenttest.Turn) (*agenttest.Provider, session.Storage) {
	t.Helper()
	fake := agenttest.New(turns...)
	m := session.Manager{Root: t.TempDir()}
	store, err := m.Create(cwd)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	ws := workset.New()
	reg := tools.NewRegistry()
	jobs, err := tools.RegisterBuiltins(reg, tools.Options{Cwd: cwd, Observer: ws})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(jobs.KillAll)

	model := ai.Model{API: "test", Provider: "test", ID: "scripted", ContextWindow: 200_000, MaxOutput: 8192}
	ctrl := compact.NewController(compact.ControllerConfig{
		Store:      store,
		WS:         ws,
		Summarizer: &compact.LLMSummarizer{Model: model, Stream: fake.Stream},
		Model:      model,
		KeepRecent: keepRecent,
		Enabled:    true,
	})
	a := agent.New(agent.Config{
		Model:   model,
		Tools:   reg,
		Stream:  fake.Stream,
		Hooks:   ctrl.Hooks(),
		History: ctrl.LoadHistory(),
	})
	ctrl.SetNotify(a.Notify)

	var out, errOut strings.Builder
	if code := RunPrint(context.Background(), PrintDeps{Agent: a, Out: &out, ErrOut: &errOut}, opts); code != ExitOK {
		t.Fatalf("exit = %d\nstdout: %s\nstderr: %s", code, out.String(), errOut.String())
	}
	return fake, store
}

func contextTexts(c ai.Context) string {
	var b strings.Builder
	for _, m := range c.Messages {
		switch v := m.(type) {
		case ai.UserMessage:
			for _, cc := range v.Content {
				if tc, ok := cc.(ai.TextContent); ok {
					b.WriteString(tc.Text + "\n")
				}
			}
		case ai.AssistantMessage:
			for _, cc := range v.Content {
				if tc, ok := cc.(ai.TextContent); ok {
					b.WriteString(tc.Text + "\n")
				}
			}
		case ai.ToolResultMessage:
			for _, cc := range v.Content {
				if tc, ok := cc.(ai.TextContent); ok {
					b.WriteString(tc.Text + "\n")
				}
			}
		}
	}
	return b.String()
}

// TestE2EThresholdCompaction drives a session over the auto-compaction
// threshold via scripted usage: the post-turn trigger summarizes old
// history, the entry lands in the session file, and the next request goes
// out as summary + kept tail.
func TestE2EThresholdCompaction(t *testing.T) {
	cwd := t.TempDir()
	longText := strings.Repeat("the assistant elaborates at length ", 24) // ~840 chars: the kept tail
	fake, store := e2eRun(t, cwd,
		PrintOptions{Prompt: "ORIGINAL-PROMPT-MARKER " + strings.Repeat("context ", 50), Messages: []string{"and now continue"}},
		100, // keepRecent: tiny, so the cut succeeds in a short session
		// Call 1: turn 1 answer; usage pushes the estimate over
		// window − reserve (200000 − 16384).
		agenttest.Turn{Text: longText, Usage: ai.Usage{Total: 190_000}},
		// Call 2: the summarizer request issued by the post-turn trigger.
		agenttest.Turn{Text: "E2E-SUMMARY of the early conversation"},
		// Call 3: the follow-up turn, built from the compacted context.
		agenttest.Turn{Text: "continuing"},
	)

	if fake.Calls() != 3 {
		t.Fatalf("provider calls = %d, want 3 (turn, summarize, turn)", fake.Calls())
	}
	// The summarizer call carried the serialized old conversation.
	sumReq := contextTexts(fake.Contexts[1])
	if !strings.Contains(sumReq, "<conversation>") || !strings.Contains(sumReq, "ORIGINAL-PROMPT-MARKER") {
		t.Fatalf("summarizer request malformed:\n%s", sumReq[:200])
	}

	// Session file gained the compaction entry.
	var ce *session.CompactionEntry
	for _, e := range store.Entries() {
		if v, ok := e.(*session.CompactionEntry); ok {
			ce = v
		}
	}
	if ce == nil {
		t.Fatal("no compaction entry persisted")
	}
	if !strings.Contains(ce.Summary, "E2E-SUMMARY") || ce.TokensBefore < 100_000 {
		t.Fatalf("compaction entry = %+v", ce)
	}

	// The follow-up request contains the summary plus the kept tail, and the
	// summarized prompt is gone.
	next := contextTexts(fake.Contexts[2])
	if !strings.Contains(next, "<summary>") || !strings.Contains(next, "E2E-SUMMARY") {
		t.Fatalf("next request lacks summary:\n%s", next)
	}
	if !strings.Contains(next, longText) {
		t.Fatal("kept tail missing from next request")
	}
	if strings.Contains(next, "ORIGINAL-PROMPT-MARKER") {
		t.Fatal("summarized prompt leaked into the next request")
	}
	if !strings.Contains(next, "and now continue") {
		t.Fatal("follow-up prompt missing from next request")
	}
}

// TestE2EStalenessNotice exercises the working set through real tools: a
// read stamps the file, an out-of-band bash append invalidates it, the next
// request carries the notice, and a re-read clears it.
func TestE2EStalenessNotice(t *testing.T) {
	cwd := t.TempDir()
	target := filepath.Join(cwd, "watched.txt")
	if err := os.WriteFile(target, []byte("v1 content\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	fake, store := e2eRun(t, cwd,
		PrintOptions{Prompt: "inspect the file"},
		0, // default keepRecent — no compaction in this test
		agenttest.Turn{ToolCalls: []agenttest.ToolCallSpec{{ID: "t1", Name: "read", Args: `{"path":"watched.txt"}`}}},
		agenttest.Turn{ToolCalls: []agenttest.ToolCallSpec{{ID: "t2", Name: "bash", Args: `{"command":"echo out-of-band >> watched.txt"}`}}},
		agenttest.Turn{ToolCalls: []agenttest.ToolCallSpec{{ID: "t3", Name: "read", Args: `{"path":"watched.txt"}`}}},
		agenttest.Turn{Text: "all checked"},
	)

	if fake.Calls() != 4 {
		t.Fatalf("provider calls = %d, want 4", fake.Calls())
	}
	// Request for turn 3 (after the bash modification): notice present.
	withNotice := contextTexts(fake.Contexts[2])
	if !strings.Contains(withNotice, "[system] Files changed on disk since you last read them") ||
		!strings.Contains(withNotice, "watched.txt (read turn 1, modified)") {
		t.Fatalf("staleness notice missing or wrong:\n%s", withNotice)
	}
	// Request for turn 4 (after the re-read): notice cleared.
	if after := contextTexts(fake.Contexts[3]); strings.Contains(after, "[system] Files changed on disk") {
		t.Fatal("staleness notice persists after re-read")
	}

	// Working-set state was persisted as a custom entry.
	found := false
	for _, e := range store.Entries() {
		if v, ok := e.(*session.CustomEntry); ok && v.CustomType == "workingset" {
			found = true
		}
	}
	if !found {
		t.Fatal("no workingset custom entry persisted")
	}
}
