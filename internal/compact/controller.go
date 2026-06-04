package compact

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"

	"github.com/user/mixi-agent/internal/agent"
	"github.com/user/mixi-agent/internal/ai"
	"github.com/user/mixi-agent/internal/session"
	"github.com/user/mixi-agent/internal/workset"
)

// worksetCustomType tags working-set state entries in the session file.
const worksetCustomType = "workingset"

// ControllerConfig assembles a Controller.
type ControllerConfig struct {
	Store      session.Storage
	WS         *workset.WorkingSet
	Summarizer Summarizer
	Model      ai.Model
	MaxTokens  int // per-request output budget; 0 → Model.MaxOutput
	Reserve    int // 0 → DefaultReserveTokens
	KeepRecent int // 0 → DefaultKeepRecentTokens
	Enabled    bool
	Log        *slog.Logger
}

// Controller is the context-management bridge between the agent loop and the
// session: it persists messages synchronously, mirrors the conversation with
// its session entry IDs, builds each request's context, and runs compaction
// on its three triggers (post-turn, pre-flight budget, overflow recovery).
type Controller struct {
	cfg       ControllerConfig
	builder   *workset.Builder
	compactor *Compactor
	window    int
	reserve   int
	log       *slog.Logger

	mu     sync.Mutex
	notify func(agent.Event)
	msgs   []agent.AgentMessage // mirror of the agent conversation
	ids    []string             // session entry ID per message ("" if unpersisted)
	pinned map[int]bool         // message index → pinned
	// anchorFresh: a provider-reported usage total arrived after the last
	// compaction. Without it the anchored estimate still reflects the
	// pre-compaction context, so auto-triggering on it would loop.
	anchorFresh bool
	// compactedThisTurn rate-limits the pre-flight (build-time) trigger to
	// one compaction per turn; trim handles the rest.
	compactedThisTurn bool
}

// NewController wires the builder, compactor and working set together.
func NewController(cfg ControllerConfig) *Controller {
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	if cfg.Reserve <= 0 {
		cfg.Reserve = DefaultReserveTokens
	}
	if cfg.KeepRecent <= 0 {
		cfg.KeepRecent = DefaultKeepRecentTokens
	}
	maxTokens := cfg.MaxTokens
	if maxTokens <= 0 {
		maxTokens = cfg.Model.MaxOutput
	}
	c := &Controller{
		cfg:     cfg,
		window:  cfg.Model.ContextWindow,
		reserve: cfg.Reserve,
		log:     cfg.Log,
		pinned:  map[int]bool{},
		compactor: &Compactor{
			Summarizer: cfg.Summarizer,
			KeepRecent: cfg.KeepRecent,
			Reserve:    cfg.Reserve,
			MaxOutput:  cfg.Model.MaxOutput,
		},
	}
	c.builder = &workset.Builder{
		WS:          cfg.WS,
		Window:      cfg.Model.ContextWindow,
		Reserve:     cfg.Reserve,
		MaxOutput:   maxTokens,
		Estimate:    c.estimate,
		EstimateOne: EstimateAgentMessage,
	}
	if cfg.Enabled {
		c.builder.CompactNow = c.compactForBuild
	}
	if cfg.WS != nil {
		cfg.WS.SetTurn(1)
	}
	return c
}

// SetNotify installs the event publisher (agent.Notify) once the agent
// exists; events before that are silently dropped.
func (c *Controller) SetNotify(fn func(agent.Event)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.notify = fn
}

func (c *Controller) emit(ev agent.Event) {
	c.mu.Lock()
	fn := c.notify
	c.mu.Unlock()
	if fn != nil {
		fn(ev)
	}
}

// Hooks returns the loop hooks this controller implements. The caller may
// fill the remaining hook fields before passing them to the agent.
func (c *Controller) Hooks() agent.Hooks {
	return agent.Hooks{
		TransformContext:  c.TransformContext,
		OnMessage:         c.OnMessage,
		AfterTurn:         c.AfterTurn,
		OnContextOverflow: c.OnContextOverflow,
	}
}

// LoadHistory replays the session path into agent messages (the resumed
// conversation), restoring working-set state and the compaction projection.
// Call once, before the agent starts.
func (c *Controller) LoadHistory() []agent.AgentMessage {
	path := c.cfg.Store.PathToRoot(c.cfg.Store.LeafID())
	var msgs []agent.AgentMessage
	var ids []string
	pinned := map[int]bool{}
	anchorFresh := false
	var lastComp *session.CompactionEntry
	for _, e := range path {
		switch v := e.(type) {
		case *session.MessageEntry:
			msgs = append(msgs, agent.ModelMessage{Msg: v.Message})
			ids = append(ids, v.EntryID())
			if v.Pinned {
				pinned[len(msgs)-1] = true
			}
			if am, ok := v.Message.(ai.AssistantMessage); ok && am.Usage.Total > 0 {
				anchorFresh = true
			}
		case *session.CustomMessageEntry:
			msgs = append(msgs, agent.CustomMessage{Content: customText(v.Content)})
			ids = append(ids, v.EntryID())
			if v.Pinned {
				pinned[len(msgs)-1] = true
			}
		case *session.BranchSummaryEntry:
			msgs = append(msgs, agent.BranchSummary{Summary: v.Summary})
			ids = append(ids, v.EntryID())
		case *session.CustomEntry:
			if v.CustomType == worksetCustomType && c.cfg.WS != nil {
				if err := c.cfg.WS.Restore(v.Data); err != nil {
					c.log.Warn("working-set restore failed; starting empty", "err", err)
				}
			}
		case *session.CompactionEntry:
			lastComp = v
			anchorFresh = false
		}
	}

	c.mu.Lock()
	c.msgs, c.ids, c.pinned, c.anchorFresh = msgs, ids, pinned, anchorFresh
	c.mu.Unlock()
	if lastComp != nil {
		c.installCompactionState(lastComp)
	}
	return append([]agent.AgentMessage(nil), msgs...)
}

// customText unwraps a custom_message content payload: JSON string or raw.
func customText(raw []byte) string {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return string(raw)
}

// OnMessage mirrors and persists every message the loop records, in order
// and synchronously — by the time the next request is built, the session
// already contains the turn.
func (c *Controller) OnMessage(m agent.AgentMessage) {
	id := ""
	if mm, ok := m.(agent.ModelMessage); ok {
		e := &session.MessageEntry{Message: mm.Msg}
		if err := c.cfg.Store.Append(e); err != nil {
			c.log.Error("session append failed; conversation continues unsaved", "error", err)
		} else {
			id = e.EntryID()
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.msgs = append(c.msgs, m)
	c.ids = append(c.ids, id)
	if mm, ok := m.(agent.ModelMessage); ok {
		if am, ok := mm.Msg.(ai.AssistantMessage); ok && am.Usage.Total > 0 {
			c.anchorFresh = true
		}
	}
}

// estimate picks the estimator the anchor state allows: provider-reported
// usage is ground truth until a compaction invalidates it, then the pure
// heuristic carries one turn until a fresh usage total arrives.
func (c *Controller) estimate(msgs []agent.AgentMessage) int {
	c.mu.Lock()
	fresh := c.anchorFresh
	c.mu.Unlock()
	if fresh {
		return EstimateContext(msgs)
	}
	return EstimateContextPure(msgs)
}

// TransformContext builds the request projection (the [STREAM] hook).
func (c *Controller) TransformContext(ctx context.Context, msgs []agent.AgentMessage) ([]agent.AgentMessage, error) {
	return c.builder.Build(ctx, msgs)
}

// AfterTurn advances the working set, persists its state, and runs the
// post-turn auto-compaction check.
func (c *Controller) AfterTurn(ctx context.Context, turn int) error {
	if c.cfg.WS != nil {
		c.cfg.WS.SetTurn(turn + 1)
		if raw, changed, err := c.cfg.WS.Snapshot(); err != nil {
			c.log.Warn("working-set snapshot failed", "err", err)
		} else if changed {
			ce := &session.CustomEntry{CustomType: worksetCustomType, Data: raw}
			if err := c.cfg.Store.Append(ce); err != nil {
				c.log.Error("working-set persistence failed", "error", err)
			}
		}
	}

	c.mu.Lock()
	c.compactedThisTurn = false
	fresh := c.anchorFresh
	estimate := EstimateContext(c.msgs)
	c.mu.Unlock()

	if !c.cfg.Enabled || !fresh {
		return nil
	}
	threshold := c.window - c.reserve
	c.log.Debug("post-turn context estimate", "estimate", estimate, "threshold", threshold)
	if estimate <= threshold {
		return nil
	}
	if err := c.compact(ctx, ""); err != nil && !errors.Is(err, ErrNothingToCompact) {
		return err
	}
	return nil
}

// OnContextOverflow handles a provider context-overflow rejection: compact
// once and let the loop retry the request.
func (c *Controller) OnContextOverflow(ctx context.Context) bool {
	if !c.cfg.Enabled {
		return false
	}
	if err := c.compact(ctx, ""); err != nil {
		c.log.Error("overflow recovery compaction failed", "error", err)
		return false
	}
	return true
}

// Compact runs a manual compaction with optional focus instructions
// (the /compact command).
func (c *Controller) Compact(ctx context.Context, focus string) error {
	return c.compact(ctx, focus)
}

// compactForBuild is the builder's pre-flight stage, rate-limited to once
// per turn so a Build→compact→Build cycle cannot loop.
func (c *Controller) compactForBuild(ctx context.Context) error {
	c.mu.Lock()
	done := c.compactedThisTurn
	c.mu.Unlock()
	if done {
		return errors.New("compact: already compacted this turn")
	}
	return c.compact(ctx, "")
}

// compact performs one compaction and installs the resulting projection
// state. Failures never touch the session; the caller decides the fallback.
func (c *Controller) compact(ctx context.Context, focus string) error {
	c.emit(agent.EvCompactionStart{})
	var read, written []string
	if c.cfg.WS != nil {
		read, written = c.cfg.WS.Lists()
	}
	entry, err := c.compactor.Compact(ctx, c.cfg.Store, Request{Focus: focus, Read: read, Written: written})
	if err != nil {
		if !errors.Is(err, ErrNothingToCompact) {
			c.log.Error("compaction failed", "error", err)
			c.emit(agent.EvNotice{Text: "Compaction failed: " + err.Error()})
		}
		return err
	}
	c.installCompactionState(entry)
	c.mu.Lock()
	c.compactedThisTurn = true
	c.anchorFresh = false
	c.mu.Unlock()
	c.emit(agent.EvCompactionEnd{Summary: entry.Summary, TokensBefore: entry.TokensBefore})
	return nil
}

// installCompactionState maps a compaction entry onto the in-memory mirror:
// the first kept message index and the pinned messages that must outlive the
// cut, then hands both to the builder.
func (c *Controller) installCompactionState(entry *session.CompactionEntry) {
	c.mu.Lock()
	keptFrom := c.keptFromLocked(entry.FirstKeptEntryID)
	var pinnedMsgs []agent.AgentMessage
	for i := 0; i < keptFrom && i < len(c.msgs); i++ {
		if c.pinned[i] {
			pinnedMsgs = append(pinnedMsgs, c.msgs[i])
		}
	}
	c.mu.Unlock()
	c.builder.SetCompactionState(entry.Summary, keptFrom, pinnedMsgs)
}

// keptFromLocked resolves a session entry ID to a message index. When the
// cut sits on a non-message entry (settings change), the first message after
// it on the path is the kept boundary.
func (c *Controller) keptFromLocked(firstKept string) int {
	if i := indexOfString(c.ids, firstKept); i >= 0 {
		return i
	}
	path := c.cfg.Store.PathToRoot(c.cfg.Store.LeafID())
	seen := false
	for _, e := range path {
		if e.EntryID() == firstKept {
			seen = true
			continue
		}
		if !seen {
			continue
		}
		if i := indexOfString(c.ids, e.EntryID()); i >= 0 {
			return i
		}
	}
	return len(c.msgs)
}

func indexOfString(list []string, s string) int {
	if s == "" {
		return -1
	}
	for i, v := range list {
		if v == s {
			return i
		}
	}
	return -1
}
