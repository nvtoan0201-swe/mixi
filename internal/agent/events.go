package agent

import (
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/user/mixi-agent/internal/ai"
)

// Event is the sealed union of agent runtime events.
//
// Order contract per run:
//
//	EvAgentStart, then per turn: EvTurnStart, EvMessageStart,
//	EvMessageUpdate*, EvMessageEnd (assistant), then tool events
//	(EvToolStart per call in source order for sequential — interleaved for
//	parallel; EvToolEnd in completion order), then EvTurnEnd. EvRetryStart/
//	EvRetryEnd may precede a turn's EvMessageStart. Exactly one EvAgentEnd
//	terminates the run. Steering/injected messages emit EvMessageStart+End
//	pairs before EvTurnStart.
type Event interface{ isEvent() }

// EndReason explains why a run terminated.
type EndReason string

const (
	EndDone     EndReason = "done"
	EndAborted  EndReason = "aborted"
	EndError    EndReason = "error"
	EndMaxTurns EndReason = "maxTurns"
)

type EvAgentStart struct{}

// EvAgentEnd carries every new AgentMessage produced by the run.
type EvAgentEnd struct {
	Reason   EndReason
	Messages []AgentMessage
}

type EvTurnStart struct{ Turn int }

type EvTurnEnd struct {
	Turn        int
	Message     ai.AssistantMessage
	ToolResults []ai.ToolResultMessage
}

type EvMessageStart struct{ Msg AgentMessage }

// EvMessageUpdate forwards a raw provider stream event (render-only).
type EvMessageUpdate struct{ StreamEvent ai.StreamEvent }

type EvMessageEnd struct{ Msg AgentMessage }

type EvToolStart struct{ Call ai.ToolCall }

// EvToolUpdate carries partial tool output (render-only).
type EvToolUpdate struct {
	CallID  string
	Partial string
}

type EvToolEnd struct {
	CallID string
	Result ai.ToolResultMessage
}

type EvRetryStart struct {
	Attempt int
	Max     int
	Delay   time.Duration
	Err     string
}

type EvRetryEnd struct {
	OK      bool
	Attempt int
}

type EvCompactionStart struct{}

type EvCompactionEnd struct {
	Summary      string
	TokensBefore int
}

// EvPermissionAsk surfaces a pending permission request; Req is the
// permission engine's request payload (typed when that package lands).
type EvPermissionAsk struct{ Req any }

// EvNotice is a user-visible harness notice (max-turns hit, queue full…).
type EvNotice struct{ Text string }

// EvStatus sets a keyed footer status segment (extension status lines);
// empty Text clears the segment.
type EvStatus struct{ Key, Text string }

func (EvAgentStart) isEvent()      {}
func (EvAgentEnd) isEvent()        {}
func (EvTurnStart) isEvent()       {}
func (EvTurnEnd) isEvent()         {}
func (EvMessageStart) isEvent()    {}
func (EvMessageUpdate) isEvent()   {}
func (EvMessageEnd) isEvent()      {}
func (EvToolStart) isEvent()       {}
func (EvToolUpdate) isEvent()      {}
func (EvToolEnd) isEvent()         {}
func (EvRetryStart) isEvent()      {}
func (EvRetryEnd) isEvent()        {}
func (EvCompactionStart) isEvent() {}
func (EvCompactionEnd) isEvent()   {}
func (EvPermissionAsk) isEvent()   {}
func (EvNotice) isEvent()          {}
func (EvStatus) isEvent()          {}

// isRenderOnly reports whether an event may be dropped for slow consumers.
// Lifecycle events must reach every subscriber or the subscriber is cut.
func isRenderOnly(ev Event) bool {
	switch ev.(type) {
	case EvMessageUpdate, EvToolUpdate:
		return true
	}
	return false
}

const (
	subscriberBuf     = 256
	lifecycleBlockMax = time.Second
)

// subscriber is one fan-out target. Its own lock serializes sends against
// close so unsubscribing while a publish is in flight can never panic.
type subscriber struct {
	mu      sync.Mutex
	closed  bool
	ch      chan Event
	dropped atomic.Int64
}

// deliver applies the drop policy; false means the consumer sat on a full
// buffer past the lifecycle grace period and must be disconnected.
func (s *subscriber) deliver(ev Event, log *slog.Logger) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return true
	}
	select {
	case s.ch <- ev:
		return true
	default:
	}
	if isRenderOnly(ev) {
		n := s.dropped.Add(1)
		if n == 1 || n%100 == 0 {
			log.Warn("agent: dropping render events for slow subscriber", "dropped", n)
		}
		return true
	}
	// Lifecycle event with a full buffer: give the consumer a grace
	// period, then cut it loose rather than stalling the run.
	timer := time.NewTimer(lifecycleBlockMax)
	defer timer.Stop()
	select {
	case s.ch <- ev:
		return true
	case <-timer.C:
		return false
	}
}

// close is idempotent and safe against concurrent deliver calls.
func (s *subscriber) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.closed = true
		close(s.ch)
	}
}

// eventBus fans events out to subscribers. Policy for a full buffer:
// render-only events are dropped (counted, WARN-logged once per burst);
// lifecycle events block up to lifecycleBlockMax then the subscriber is
// disconnected so one stuck consumer cannot stall the run.
type eventBus struct {
	mu   sync.Mutex
	subs map[*subscriber]struct{}
	log  *slog.Logger
}

func newEventBus(log *slog.Logger) *eventBus {
	if log == nil {
		log = slog.Default()
	}
	return &eventBus{subs: map[*subscriber]struct{}{}, log: log}
}

// Subscribe registers a new consumer; the returned func unsubscribes and
// closes the channel. Safe to call concurrently with Publish.
func (b *eventBus) Subscribe() (<-chan Event, func()) {
	s := &subscriber{ch: make(chan Event, subscriberBuf)}
	b.mu.Lock()
	b.subs[s] = struct{}{}
	b.mu.Unlock()
	cancel := func() {
		b.remove(s)
		s.close()
	}
	return s.ch, cancel
}

// Publish delivers ev to every subscriber per the drop policy. The
// registration lock is NOT held during delivery, so a stuck consumer in its
// lifecycle grace period never blocks Subscribe/unsubscribe; it can delay
// this publisher (and later subscribers in this snapshot) by at most
// lifecycleBlockMax before being disconnected.
func (b *eventBus) Publish(ev Event) {
	b.mu.Lock()
	snapshot := make([]*subscriber, 0, len(b.subs))
	for s := range b.subs {
		snapshot = append(snapshot, s)
	}
	b.mu.Unlock()

	for _, s := range snapshot {
		if !s.deliver(ev, b.log) {
			b.remove(s)
			s.close()
			b.log.Error("agent: disconnected slow subscriber on lifecycle event", "dropped", s.dropped.Load())
		}
	}
}

func (b *eventBus) remove(s *subscriber) {
	b.mu.Lock()
	delete(b.subs, s)
	b.mu.Unlock()
}

// CloseAll disconnects every subscriber (agent shutdown).
func (b *eventBus) CloseAll() {
	b.mu.Lock()
	snapshot := make([]*subscriber, 0, len(b.subs))
	for s := range b.subs {
		delete(b.subs, s)
		snapshot = append(snapshot, s)
	}
	b.mu.Unlock()
	for _, s := range snapshot {
		s.close()
	}
}
