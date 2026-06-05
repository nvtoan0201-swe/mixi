package obs

import (
	"fmt"
	"io"
	"time"

	"github.com/user/mixi-agent/internal/agent"
	"github.com/user/mixi-agent/internal/ai"
	"github.com/user/mixi-agent/internal/session"
)

// ReplayOptions configures one replay run.
type ReplayOptions struct {
	Speed string // "1x" (default) | "5x" | "instant"
	Until string // entry ID to stop after; "" replays the whole path
	Out   io.Writer
}

// RunReplay renders a recorded session file to Out with zero API calls. The
// file is opened read-only without the advisory lock, so a session that is
// still live in another process can be inspected.
func RunReplay(path string, o ReplayOptions) error {
	store, err := session.OpenJSONL(path, session.Options{ReadOnly: true})
	if err != nil {
		return err
	}
	defer store.Close()
	return Replay(store, o)
}

// Replay renders the store's active path (root → current leaf): each entry
// is synthesized into the agent-event sequence a live run would have
// produced, then rendered as a plain-text transcript. Pacing follows the
// recorded timestamps scaled by Speed.
func Replay(store session.Storage, o ReplayOptions) error {
	pace, err := parseSpeed(o.Speed)
	if err != nil {
		return err
	}
	entries := store.PathToRoot(store.LeafID())
	if len(entries) == 0 {
		return fmt.Errorf("replay: session has no entries")
	}
	r := &transcript{out: o.Out}
	var prev time.Time
	turn := 0
	for _, e := range entries {
		ts, terr := time.Parse(time.RFC3339Nano, e.EntryTime())
		if terr == nil {
			pace.sleep(prev, ts)
			prev = ts
		}
		for _, ev := range entryEvents(e, &turn) {
			r.Emit(ev)
		}
		if r.err != nil {
			return fmt.Errorf("replay: write: %w", r.err)
		}
		if o.Until != "" && e.EntryID() == o.Until {
			return nil
		}
	}
	if o.Until != "" {
		return fmt.Errorf("replay: --until entry %q is not on the active path", o.Until)
	}
	return nil
}

// maxReplayGap caps inter-entry sleeps so long idle periods in a recording
// (user away, slow tools) never stall a paced replay.
const maxReplayGap = 2 * time.Second

// pacer scales recorded inter-entry gaps; div 0 means instant.
type pacer struct{ div time.Duration }

func parseSpeed(s string) (pacer, error) {
	switch s {
	case "", "1x":
		return pacer{div: 1}, nil
	case "5x":
		return pacer{div: 5}, nil
	case "instant":
		return pacer{}, nil
	}
	return pacer{}, fmt.Errorf("replay: invalid speed %q (1x|5x|instant)", s)
}

func (p pacer) sleep(prev, cur time.Time) {
	if p.div == 0 || prev.IsZero() || !cur.After(prev) {
		return
	}
	d := cur.Sub(prev) / p.div
	if d > maxReplayGap {
		d = maxReplayGap
	}
	time.Sleep(d)
}

// entryEvents synthesizes the render-driving event subset for one recorded
// entry: message entries become start/end pairs with their recorded content,
// tool results pair to their calls by ID. Off-chain entry types (labels,
// leaves) never appear on the active path; entries with no render form
// (custom state) yield nothing.
func entryEvents(e session.Entry, turn *int) []agent.Event {
	switch v := e.(type) {
	case *session.MessageEntry:
		switch m := v.Message.(type) {
		case ai.UserMessage:
			am := agent.ModelMessage{Msg: m}
			return []agent.Event{agent.EvMessageStart{Msg: am}, agent.EvMessageEnd{Msg: am}}
		case ai.AssistantMessage:
			*turn++
			am := agent.ModelMessage{Msg: m}
			evs := []agent.Event{
				agent.EvTurnStart{Turn: *turn},
				agent.EvMessageStart{Msg: am},
				agent.EvMessageEnd{Msg: am},
			}
			for _, c := range m.Content {
				if tc, ok := c.(ai.ToolCall); ok {
					evs = append(evs, agent.EvToolStart{Call: tc})
				}
			}
			return evs
		case ai.ToolResultMessage:
			return []agent.Event{agent.EvToolEnd{CallID: m.ToolCallID, Result: m}}
		}
	case *session.CompactionEntry:
		return []agent.Event{agent.EvCompactionEnd{Summary: v.Summary, TokensBefore: v.TokensBefore}}
	case *session.BranchSummaryEntry:
		return []agent.Event{agent.EvNotice{Text: "branch summary: " + v.Summary}}
	case *session.ModelChangeEntry:
		return []agent.Event{agent.EvNotice{Text: "model → " + v.Provider + "/" + v.ModelID}}
	case *session.ThinkingLevelChangeEntry:
		return []agent.Event{agent.EvNotice{Text: "thinking → " + string(v.ThinkingLevel)}}
	case *session.CustomMessageEntry:
		if v.Display {
			return []agent.Event{agent.EvNotice{Text: v.CustomType + ": " + string(v.Content)}}
		}
	}
	return nil
}
