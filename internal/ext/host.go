package ext

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/user/mixi-agent/internal/agent"
	"github.com/user/mixi-agent/internal/tools"
)

// Registrar receives extension tools; *tools.Registry satisfies it.
type Registrar interface {
	Register(t tools.Tool) error
}

// ReadyInfo is the session context sent in the ready message.
type ReadyInfo struct {
	SessionID string
	Cwd       string
	Mode      string
	Model     string
}

// Options configures the extension fleet.
type Options struct {
	Configs  map[string]Config
	Registry Registrar
	Log      *slog.Logger // nil = default
	// Notify surfaces user-facing notices (extension disabled, …). May be nil.
	Notify func(text string)
	Ready  ReadyInfo

	// Test seams; zero values select production behavior.
	Backoff      []time.Duration
	StableAfter  time.Duration // uptime that resets the crash counter
	HelloWait    time.Duration
	BlockTimeout time.Duration
}

// Host supervises every configured extension: lifecycle, restarts, event
// fan-out, blocking gates, and action dispatch. It implements
// agent.ToolCallFilter for the gate (see gate.go).
type Host struct {
	reg          Registrar
	log          *slog.Logger
	notify       func(string)
	ready        ReadyInfo
	backoff      []time.Duration
	stableAfter  time.Duration
	helloWait    time.Duration
	blockTimeout time.Duration

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// exts is write-once before Start; order is the deterministic gate and
	// registration order (sorted names).
	exts  map[string]*extension
	order []string

	bindMu  sync.Mutex
	api     API
	actBuf  []bufferedAction // actions arriving before Bind
	stopped bool
}

// bufferedAction holds a pre-Bind action until the API exists.
type bufferedAction struct {
	ext *extension
	msg ActionMsg
}

// NewHost validates the config and prepares the fleet without starting it.
func NewHost(opts Options) (*Host, error) {
	h := &Host{
		reg:          opts.Registry,
		log:          opts.Log,
		notify:       opts.Notify,
		ready:        opts.Ready,
		backoff:      opts.Backoff,
		stableAfter:  opts.StableAfter,
		helloWait:    opts.HelloWait,
		blockTimeout: opts.BlockTimeout,
		exts:         map[string]*extension{},
		order:        orderedNames(opts.Configs),
	}
	if h.log == nil {
		h.log = slog.Default()
	}
	if h.notify == nil {
		h.notify = func(string) {}
	}
	if len(h.backoff) == 0 {
		h.backoff = []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}
	}
	if h.stableAfter <= 0 {
		h.stableAfter = 30 * time.Second
	}
	if h.helloWait <= 0 {
		h.helloWait = helloTimeout
	}
	if h.blockTimeout <= 0 {
		h.blockTimeout = blockingTimeout
	}
	for _, name := range h.order {
		h.exts[name] = newExtension(name, opts.Configs[name])
	}
	return h, nil
}

// Start launches one supervisor per extension and returns immediately.
func (h *Host) Start(ctx context.Context) {
	h.ctx, h.cancel = context.WithCancel(ctx)
	for _, name := range h.order {
		e := h.exts[name]
		h.wg.Add(1)
		go h.supervise(e)
	}
}

// WaitInitial blocks until every extension resolved its first handshake
// (ready or failure) or ctx expires; stragglers keep settling in the
// background either way.
func (h *Host) WaitInitial(ctx context.Context) {
	for _, name := range h.order {
		select {
		case <-h.exts[name].settled:
		case <-ctx.Done():
			return
		}
	}
}

// Bind connects the host to the live session: the action API target and
// the agent event stream. It flushes actions buffered during startup,
// announces session_start, and starts the fan-out pump.
func (h *Host) Bind(api API, events <-chan agent.Event) {
	h.bindMu.Lock()
	h.api = api
	buf := h.actBuf
	h.actBuf = nil
	h.bindMu.Unlock()
	for _, a := range buf {
		h.dispatchAction(a.ext, a.msg)
	}
	h.fanOut(mkEvent(EvSessionStart, struct{}{}))
	h.wg.Add(1)
	go func() {
		defer h.wg.Done()
		for {
			select {
			case <-h.ctx.Done():
				return // Close waits on this goroutine; never outlive the host
			case ev, ok := <-events:
				if !ok {
					return
				}
				for _, he := range busEvents(ev) {
					h.fanOut(he)
				}
			}
		}
	}()
}

// fanOut queues one event to every subscribed, alive extension.
func (h *Host) fanOut(he hostEvent) {
	for _, name := range h.order {
		e := h.exts[name]
		switch e.getState() {
		case StateReady, StateDegraded:
		default:
			continue
		}
		if !e.subscribed(he.name) {
			continue
		}
		if e.enqueue(EventMsg{Type: TypeEvent, Event: he.name, Payload: he.payload}) {
			h.log.Warn("ext: event queue overflow, dropped oldest", "ext", e.name, "event", he.name)
		}
	}
}

// Close announces session_shutdown, stops every extension with the
// shutdown escalation, and waits for the supervisors to exit.
func (h *Host) Close() {
	h.bindMu.Lock()
	if h.stopped {
		h.bindMu.Unlock()
		return
	}
	h.stopped = true
	h.bindMu.Unlock()
	for _, name := range h.order {
		e := h.exts[name]
		if st := e.getState(); st == StateReady || st == StateDegraded {
			if e.subscribed(EvSessionShutdown) {
				e.sendDirect(EventMsg{Type: TypeEvent, Event: EvSessionShutdown, Payload: []byte("{}")})
			}
		}
	}
	if h.cancel != nil {
		h.cancel()
	}
	h.wg.Wait()
}

// Status reports every extension in deterministic order.
type Status struct {
	Name  string
	State ExtState
	Tools int
}

func (h *Host) Status() []Status {
	out := make([]Status, 0, len(h.order))
	for _, name := range h.order {
		e := h.exts[name]
		e.mu.Lock()
		out = append(out, Status{Name: name, State: e.state, Tools: len(e.tools)})
		e.mu.Unlock()
	}
	return out
}

// Command is one extension-registered slash command for UI autocomplete.
type Command struct {
	Ext, Name, Description string
}

// Commands lists registered commands in deterministic order.
func (h *Host) Commands() []Command {
	var out []Command
	for _, name := range h.order {
		e := h.exts[name]
		e.mu.Lock()
		for _, c := range e.commands {
			out = append(out, Command{Ext: name, Name: c.Name, Description: c.Description})
		}
		e.mu.Unlock()
	}
	return out
}

// DispatchCommand routes a user-invoked /command to its owning extension
// as a user_input event.
func (h *Host) DispatchCommand(name, args string) error {
	for _, extName := range h.order {
		e := h.exts[extName]
		e.mu.Lock()
		owns := false
		for _, c := range e.commands {
			if c.Name == name {
				owns = true
				break
			}
		}
		e.mu.Unlock()
		if !owns {
			continue
		}
		text := "/" + name
		if args != "" {
			text += " " + args
		}
		ev := mkEvent(EvUserInput, map[string]any{"text": text, "command": name})
		e.enqueue(EventMsg{Type: TypeEvent, Event: ev.name, Payload: ev.payload})
		return nil
	}
	return fmt.Errorf("ext: no extension owns command /%s", name)
}
