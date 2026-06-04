package compact

import (
	"context"
	"errors"
	"fmt"

	"github.com/user/mixi-agent/internal/ai"
	"github.com/user/mixi-agent/internal/session"
)

// ErrNothingToCompact means the session path holds less than keepRecent
// tokens beyond the previous compaction — there is no window to summarize.
var ErrNothingToCompact = errors.New("compact: nothing to compact")

// Compactor summarizes the old part of a session path into a compaction
// entry. The session is only written on success: a summarization failure
// leaves the file byte-identical.
type Compactor struct {
	Summarizer Summarizer
	KeepRecent int // tokens kept verbatim; 0 → DefaultKeepRecentTokens
	Reserve    int // headroom budget the summary must fit in; 0 → DefaultReserveTokens
	MaxOutput  int // model output cap; 0 → uncapped
}

// Request parameterizes one compaction run.
type Request struct {
	Focus   string   // extra summarizer instructions (manual /compact)
	Read    []string // working-set: paths read this session
	Written []string // working-set: paths written or edited this session
}

// Compact selects the cut point on the store's current path, summarizes
// everything before it (two calls when the cut splits a turn), and appends
// the compaction entry. Returns ErrNothingToCompact when the path is already
// within keepRecent.
func (c *Compactor) Compact(ctx context.Context, store session.Storage, req Request) (*session.CompactionEntry, error) {
	keep := c.KeepRecent
	if keep <= 0 {
		keep = DefaultKeepRecentTokens
	}
	reserve := c.Reserve
	if reserve <= 0 {
		reserve = DefaultReserveTokens
	}
	maxTokens := reserve * 8 / 10
	if c.MaxOutput > 0 && c.MaxOutput < maxTokens {
		maxTokens = c.MaxOutput
	}

	path := store.PathToRoot(store.LeafID())
	plan, err := planCut(path, keep)
	if err != nil {
		return nil, err
	}

	basePrompt := summarizationPrompt
	if plan.prevSummary != "" {
		basePrompt = updateSummarizationPrompt
	}
	hasPinned := anyPinned(path[:plan.cutIdx])

	historyEnd := plan.cutIdx
	if plan.splitTurn {
		historyEnd = plan.turnStartIdx
	}
	conv := conversationBlock(serializeEntries(path[plan.windowStart:historyEnd]), plan.prevSummary)
	prompt := assemblePrompt(basePrompt, req.Focus, hasPinned)
	summary, err := c.Summarizer.Summarize(ctx, conv, prompt, maxTokens)
	if err != nil {
		return nil, fmt.Errorf("compact: summarize history: %w", err)
	}

	if plan.splitTurn {
		prefixConv := conversationBlock(serializeEntries(path[plan.turnStartIdx:plan.cutIdx]), "")
		prefix, err := c.Summarizer.Summarize(ctx, prefixConv, assemblePrompt(turnPrefixPrompt, req.Focus, hasPinned), maxTokens)
		if err != nil {
			return nil, fmt.Errorf("compact: summarize turn prefix: %w", err)
		}
		summary = summary + "\n---\n" + prefix
	}

	entry := &session.CompactionEntry{
		Summary:          summary,
		FirstKeptEntryID: path[plan.cutIdx].EntryID(),
		TokensBefore:     estimatePath(path),
		Details:          MergeFileLists(plan.prevDetails, req.Read, req.Written),
	}
	if err := store.Append(entry); err != nil {
		return nil, fmt.Errorf("compact: append entry: %w", err)
	}
	return entry, nil
}

func conversationBlock(conversation, previousSummary string) string {
	s := "<conversation>\n" + conversation + "\n</conversation>"
	if previousSummary != "" {
		s = "<previous-summary>\n" + previousSummary + "\n</previous-summary>\n\n" + s
	}
	return s
}

func assemblePrompt(base, focus string, hasPinned bool) string {
	if focus != "" {
		base += "\n\nAdditional focus: " + focus
	}
	if hasPinned {
		base += "\n\n" + pinnedExclusionNote
	}
	return base
}

// cutPlan is the resolved geometry of one compaction: indexes into the path.
type cutPlan struct {
	windowStart  int    // first entry of the to-be-summarized window
	cutIdx       int    // first kept entry
	splitTurn    bool   // cut falls mid-turn: summarize the turn prefix separately
	turnStartIdx int    // user message opening the split turn
	prevSummary  string // previous compaction's summary ("" when first)
	prevDetails  session.FileLists
}

// planCut walks the path backward accumulating per-entry estimates until
// keepRecent tokens are retained, then normalizes the cut: never at a tool
// result (the call/result pair must stay together), extended backward over
// adjacent settings entries, always after a previous compaction entry.
func planCut(path []session.Entry, keepRecent int) (cutPlan, error) {
	plan := cutPlan{}

	prevIdx := -1
	for i := len(path) - 1; i >= 0; i-- {
		if ce, ok := path[i].(*session.CompactionEntry); ok {
			prevIdx = i
			plan.prevSummary = ce.Summary
			plan.prevDetails = ce.Details
			plan.windowStart = indexOf(path, ce.FirstKeptEntryID)
			if plan.windowStart < 0 {
				return plan, fmt.Errorf("compact: previous firstKeptEntryId %q not on path", ce.FirstKeptEntryID)
			}
			break
		}
	}

	acc := 0
	cut := -1
	for i := len(path) - 1; i > plan.windowStart; i-- {
		acc += estimateEntry(path[i])
		if acc >= keepRecent {
			cut = i
			break
		}
	}
	if cut < 0 {
		return plan, ErrNothingToCompact
	}

	for cut > plan.windowStart && !validCut(path[cut]) {
		cut--
	}
	// Keep the previous compaction entry and everything after it intact:
	// cutting at or before it would re-summarize the summary.
	if cut <= plan.windowStart || cut <= prevIdx {
		return plan, ErrNothingToCompact
	}

	for cut-1 > plan.windowStart && cut-1 > prevIdx && isSettingsEntry(path[cut-1]) {
		cut--
	}
	plan.cutIdx = cut

	if !isUserMessage(path[cut]) {
		turnStart := -1
		for i := cut - 1; i >= plan.windowStart; i-- {
			if isUserMessage(path[i]) {
				turnStart = i
				break
			}
		}
		// A turn opening at the window start leaves no history before it:
		// the single main summary already covers the whole window.
		if turnStart > plan.windowStart {
			plan.splitTurn = true
			plan.turnStartIdx = turnStart
		}
	}
	return plan, nil
}

// validCut reports whether the conversation may resume from e: any
// conversational entry except a tool result, which must stay adjacent to the
// assistant message that called it.
func validCut(e session.Entry) bool {
	switch v := e.(type) {
	case *session.MessageEntry:
		return v.Message.MsgRole() != ai.RoleToolResult
	case *session.CustomMessageEntry, *session.BranchSummaryEntry:
		return true
	}
	return false
}

// isSettingsEntry matches non-conversational entries that ride along with
// the following message (model/thinking/tool-set changes, custom state).
func isSettingsEntry(e session.Entry) bool {
	switch e.(type) {
	case *session.ModelChangeEntry, *session.ThinkingLevelChangeEntry,
		*session.ActiveToolsChangeEntry, *session.CustomEntry:
		return true
	}
	return false
}

func isUserMessage(e session.Entry) bool {
	me, ok := e.(*session.MessageEntry)
	return ok && me.Message.MsgRole() == ai.RoleUser
}

func anyPinned(entries []session.Entry) bool {
	for _, e := range entries {
		switch v := e.(type) {
		case *session.MessageEntry:
			if v.Pinned {
				return true
			}
		case *session.CustomMessageEntry:
			if v.Pinned {
				return true
			}
		}
	}
	return false
}

func indexOf(path []session.Entry, id string) int {
	for i, e := range path {
		if e.EntryID() == id {
			return i
		}
	}
	return -1
}

// estimatePath is the usage-anchored estimate over a session path: the last
// assistant entry's reported usage plus estimates of everything after it.
func estimatePath(path []session.Entry) int {
	anchor := -1
	total := 0
	for i := len(path) - 1; i >= 0; i-- {
		me, ok := path[i].(*session.MessageEntry)
		if !ok {
			continue
		}
		if am, ok := me.Message.(ai.AssistantMessage); ok && am.Usage.Total > 0 {
			anchor, total = i, am.Usage.Total
			break
		}
	}
	for i := anchor + 1; i < len(path); i++ {
		total += estimateEntry(path[i])
	}
	return total
}
