package workset

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/user/mixi-agent/internal/agent"
	"github.com/user/mixi-agent/internal/ai"
)

const (
	// trimMinBytes: tool results at or under this size are never trimmed —
	// the placeholder would not buy meaningful headroom.
	trimMinBytes = 1024
	// recentTurnsProtected: tool results of the last N assistant turns stay
	// intact under context pressure; the model may still be acting on them.
	recentTurnsProtected = 2
	// staleNoticeMaxFiles caps the staleness notice length.
	staleNoticeMaxFiles = 20
)

// Builder assembles the per-request message projection: compaction summary,
// pinned survivors, kept history, staleness notice — then enforces the token
// budget by trimming old fat tool results, compacting, and finally erroring.
// All work is projection-only: the input slice, the agent's conversation,
// and the session file are never mutated.
type Builder struct {
	WS        *WorkingSet
	Window    int // model context window (tokens)
	Reserve   int // headroom held back from the window
	MaxOutput int // response budget reserved per request
	// Estimate approximates the token footprint of a whole conversation;
	// EstimateOne, of a single message (both injected from the compaction
	// package, which owns token heuristics — this package stays a leaf).
	Estimate    func(msgs []agent.AgentMessage) int
	EstimateOne func(m agent.AgentMessage) int
	// CompactNow runs a compaction and updates this builder's state via
	// SetCompactionState on success. nil disables the compaction stage.
	CompactNow func(ctx context.Context) error

	mu       sync.Mutex
	summary  string
	keptFrom int                  // index into the conversation where kept history starts
	pinned   []agent.AgentMessage // pre-cut pinned messages, re-emitted after the summary
}

// SetCompactionState installs the projection effects of a compaction: the
// summary text, the first kept message index, and pinned messages from
// before the cut.
func (b *Builder) SetCompactionState(summary string, keptFrom int, pinned []agent.AgentMessage) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.summary = summary
	b.keptFrom = keptFrom
	b.pinned = pinned
}

func (b *Builder) budget() int { return b.Window - b.Reserve - b.MaxOutput }

// Build is the TransformContext hook body: projection plus budget pipeline
// (trim → compact → hard error).
func (b *Builder) Build(ctx context.Context, msgs []agent.AgentMessage) ([]agent.AgentMessage, error) {
	out, cost := b.project(msgs)
	budget := b.budget()
	if cost <= budget {
		return out, nil
	}
	out, cost = b.trimToolResults(out, cost, budget)
	if cost <= budget {
		return out, nil
	}
	if b.CompactNow != nil {
		if err := b.CompactNow(ctx); err == nil {
			out, cost = b.project(msgs)
			if cost > budget {
				out, cost = b.trimToolResults(out, cost, budget)
			}
			if cost <= budget {
				return out, nil
			}
		}
		// Compaction failed or did not free enough: fall through to the
		// hard error with whatever trimming achieved.
	}
	return nil, fmt.Errorf("context (~%d tokens) exceeds the request budget (%d) even after trimming and compaction; shorten the input or switch to a larger-context model", cost, budget)
}

// project applies the compaction state and staleness notice, returning the
// projected messages and a cost ledger: the usage-anchored estimate of the
// full conversation, credited for dropped messages and charged for
// additions. The anchor stays ground truth even when it predates the cut.
func (b *Builder) project(msgs []agent.AgentMessage) ([]agent.AgentMessage, int) {
	b.mu.Lock()
	summary, keptFrom, pinned := b.summary, b.keptFrom, b.pinned
	b.mu.Unlock()

	cost := b.Estimate(msgs)
	var out []agent.AgentMessage
	if summary != "" && keptFrom > 0 && keptFrom <= len(msgs) {
		for _, m := range msgs[:keptFrom] {
			cost -= b.EstimateOne(m)
		}
		sum := agent.CompactionSummary{Summary: summary, Timestamp: time.Now().UnixMilli()}
		out = make([]agent.AgentMessage, 0, 1+len(pinned)+len(msgs)-keptFrom+1)
		out = append(out, sum)
		cost += b.EstimateOne(sum)
		for _, p := range pinned {
			out = append(out, p)
			cost += b.EstimateOne(p)
		}
		out = append(out, msgs[keptFrom:]...)
	} else {
		out = append(make([]agent.AgentMessage, 0, len(msgs)+1), msgs...)
	}

	if notice := b.staleNotice(); notice != "" {
		n := agent.CustomMessage{Content: notice, Timestamp: time.Now().UnixMilli()}
		out = append(out, n)
		cost += b.EstimateOne(n)
	}
	return out, cost
}

// staleNotice renders the working set's stale files as a synthetic user
// message, capped at staleNoticeMaxFiles entries.
func (b *Builder) staleNotice() string {
	if b.WS == nil {
		return ""
	}
	stale := b.WS.Stale()
	if len(stale) == 0 {
		return ""
	}
	shown := stale
	more := 0
	if len(shown) > staleNoticeMaxFiles {
		more = len(shown) - staleNoticeMaxFiles
		shown = shown[:staleNoticeMaxFiles]
	}
	parts := make([]string, len(shown))
	for i, s := range shown {
		switch s.Reason {
		case "deleted":
			parts[i] = fmt.Sprintf("%s (deleted)", s.Path)
		default:
			parts[i] = fmt.Sprintf("%s (read turn %d, modified)", s.Path, s.ReadTurn)
		}
	}
	notice := "[system] Files changed on disk since you last read them: " + strings.Join(parts, ", ")
	if more > 0 {
		notice += fmt.Sprintf(", and %d more", more)
	}
	return notice + ". Re-read before relying on their content."
}

// trimToolResults replaces the bodies of fat tool results older than the
// protected recent turns with a short placeholder, largest first, until the
// cost fits the budget. The slice is modified in place (it is the builder's
// own projection); the underlying messages are replaced, never mutated.
func (b *Builder) trimToolResults(out []agent.AgentMessage, cost, budget int) ([]agent.AgentMessage, int) {
	protect := protectedFrom(out)
	type candidate struct{ idx, size int }
	var cands []candidate
	for i := 0; i < protect; i++ {
		mm, ok := out[i].(agent.ModelMessage)
		if !ok || mm.Excluded {
			continue
		}
		tr, ok := mm.Msg.(ai.ToolResultMessage)
		if !ok {
			continue
		}
		if size := toolResultBytes(tr); size > trimMinBytes {
			cands = append(cands, candidate{i, size})
		}
	}
	sort.Slice(cands, func(a, b int) bool { return cands[a].size > cands[b].size })

	for _, c := range cands {
		if cost <= budget {
			break
		}
		mm := out[c.idx].(agent.ModelMessage)
		tr := mm.Msg.(ai.ToolResultMessage)
		repl := tr
		repl.Content = []ai.Content{ai.TextContent{Text: fmt.Sprintf(
			"[tool result trimmed after context pressure; originally %d bytes. Re-run the tool if needed.]", c.size)}}
		cost -= b.EstimateOne(mm)
		mm.Msg = repl
		cost += b.EstimateOne(mm)
		out[c.idx] = mm
	}
	return out, cost
}

// protectedFrom returns the index where the protected tail begins: the
// recentTurnsProtected-th assistant message from the end, whose tool results
// all sit after it.
func protectedFrom(msgs []agent.AgentMessage) int {
	seen := 0
	for i := len(msgs) - 1; i >= 0; i-- {
		mm, ok := msgs[i].(agent.ModelMessage)
		if !ok {
			continue
		}
		if _, ok := mm.Msg.(ai.AssistantMessage); ok {
			seen++
			if seen == recentTurnsProtected {
				return i
			}
		}
	}
	return 0
}

func toolResultBytes(tr ai.ToolResultMessage) int {
	n := 0
	for _, c := range tr.Content {
		switch v := c.(type) {
		case ai.TextContent:
			n += len(v.Text)
		case ai.ImageContent:
			n += len(v.Data)
		}
	}
	return n
}
