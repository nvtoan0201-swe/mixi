package ext

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// ExtState is one node of the extension lifecycle machine.
type ExtState string

const (
	StateStarting ExtState = "starting"
	StateReady    ExtState = "ready"
	StateDegraded ExtState = "degraded" // demoted to non-blocking delivery
	StateCrashed  ExtState = "crashed"  // restarting with backoff
	StateDisabled ExtState = "disabled"
)

const (
	// blockingTimeout bounds a gate's stdin write and its response wait
	// separately, so the worst case per extension is ~2× this value.
	blockingTimeout = 5 * time.Second
	helloTimeout    = 5 * time.Second
	queueCap        = 256 // non-blocking event FIFO per extension
	maxBlockStrikes = 3   // gate timeouts before demotion
	maxMalformed    = 10  // malformed messages per window before disable
	malformedWindow = time.Minute
)

// errExtGone reports the extension process died while a reply was pending.
var errExtGone = errors.New("ext: extension exited")

// reply carries one routed response to a host-initiated request.
type reply struct {
	eventResp *EventResponse
	toolRes   *ToolResultMsg
	err       error
}

// extension is the host-side handle for one subprocess.
type extension struct {
	name string
	cfg  Config

	mu           sync.Mutex
	state        ExtState
	proc         *proc
	subs         map[string]bool // subscribed event names; "*" = all
	tools        []ToolDef       // validated at first hello, stable after
	commands     []CommandDef
	nextID       int64
	pending      map[int64]chan reply
	blockStrikes int
	malformedAt  []time.Time
	queue        chan EventMsg // non-blocking delivery FIFO
	queueDrops   int64

	settleOnce sync.Once
	settled    chan struct{} // closed once the first ready-or-failure resolves
}

func newExtension(name string, cfg Config) *extension {
	return &extension{
		name: name, cfg: cfg, state: StateStarting,
		subs:    map[string]bool{},
		pending: map[int64]chan reply{},
		queue:   make(chan EventMsg, queueCap),
		settled: make(chan struct{}),
	}
}

func (e *extension) settle() {
	e.settleOnce.Do(func() { close(e.settled) })
}

// firstHello reports whether no hello has been adopted yet (a restart
// keeps the first catalog).
func (e *extension) firstHello() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.tools == nil && e.commands == nil
}

// sendDirect writes one message bypassing the FIFO queue (session_shutdown
// on close, ready on handshake). Best effort.
func (e *extension) sendDirect(msg any) {
	e.mu.Lock()
	p := e.proc
	e.mu.Unlock()
	if p != nil {
		p.send(msg)
	}
}

func (e *extension) getState() ExtState {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.state
}

func (e *extension) setState(st ExtState) {
	e.mu.Lock()
	e.state = st
	e.mu.Unlock()
}

// subscribed reports whether the extension wants the named event.
func (e *extension) subscribed(event string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.subs["*"] || e.subs[event]
}

// register creates a pending slot for a host-initiated request and returns
// the id to send. The channel is buffered so a late routed reply never
// blocks the read loop.
func (e *extension) register() (int64, chan reply) {
	ch := make(chan reply, 1)
	e.mu.Lock()
	e.nextID++
	id := e.nextID
	e.pending[id] = ch
	e.mu.Unlock()
	return id, ch
}

// resolve routes a reply to its pending slot; unknown ids are dropped (a
// late answer after timeout is normal, not malformed).
func (e *extension) resolve(id int64, r reply) {
	e.mu.Lock()
	ch, ok := e.pending[id]
	if ok {
		delete(e.pending, id)
	}
	e.mu.Unlock()
	if ok {
		ch <- r
	}
}

// unregister drops a pending slot after a local timeout.
func (e *extension) unregister(id int64) {
	e.mu.Lock()
	delete(e.pending, id)
	e.mu.Unlock()
}

// failPending fails every in-flight request (process died): blocking gates
// fail open, tool calls turn into IsError results.
func (e *extension) failPending() {
	e.mu.Lock()
	pend := e.pending
	e.pending = map[int64]chan reply{}
	e.mu.Unlock()
	for _, ch := range pend {
		ch <- reply{err: errExtGone}
	}
}

// enqueue adds a non-blocking event to the FIFO, dropping the oldest on
// overflow. Producers (event pump, restart re-announce, command dispatch)
// serialize under the mutex so the drop-oldest pop-then-push is atomic;
// the consumer drains the channel without the lock.
func (e *extension) enqueue(ev EventMsg) (dropped bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	select {
	case e.queue <- ev:
		return false
	default:
	}
	select {
	case <-e.queue:
	default:
	}
	select {
	case e.queue <- ev:
	default:
	}
	e.queueDrops++
	return true
}

// strikeBlocking counts one gate timeout; the third demotes the extension
// to non-blocking delivery.
func (e *extension) strikeBlocking() (demoted bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.blockStrikes++
	if e.blockStrikes >= maxBlockStrikes && e.state == StateReady {
		e.state = StateDegraded
		return true
	}
	return false
}

// strikeMalformed counts one malformed inbound message within the rolling
// window; crossing the cap disables the extension.
func (e *extension) strikeMalformed(now time.Time) (disable bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	keep := e.malformedAt[:0]
	for _, t := range e.malformedAt {
		if now.Sub(t) < malformedWindow {
			keep = append(keep, t)
		}
	}
	e.malformedAt = append(keep, now)
	return len(e.malformedAt) > maxMalformed
}

// deliverBlocking sends one blocking event and waits for its response,
// bounded by the gate budget. A timeout is reported as (nil, false).
func (e *extension) deliverBlocking(ctx context.Context, event string, payload []byte, timeout time.Duration) (*EventResponse, bool, error) {
	e.mu.Lock()
	p := e.proc
	e.mu.Unlock()
	if p == nil {
		return nil, false, errExtGone
	}
	id, ch := e.register()
	msg := EventMsg{Type: TypeEvent, ID: id, Event: event, Payload: payload, ExpectsResponse: true}
	if err := p.send(msg); err != nil {
		e.unregister(id)
		return nil, false, fmt.Errorf("ext %s: send: %w", e.name, err)
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case r := <-ch:
		if r.err != nil {
			return nil, false, r.err
		}
		return r.eventResp, true, nil
	case <-timer.C:
		e.unregister(id)
		return nil, false, nil
	case <-ctx.Done():
		e.unregister(id)
		return nil, false, ctx.Err()
	}
}
