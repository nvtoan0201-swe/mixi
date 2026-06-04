package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime"
	"sync"
	"time"

	"github.com/user/mixi-agent/internal/ai"
	"github.com/user/mixi-agent/internal/tools"
)

// ErrBusy is returned when Prompt/Continue is called while a run is active.
var ErrBusy = errors.New("agent: a run is already active")

// DefaultMaxTurns caps runaway tool loops; 0 disables the cap.
const DefaultMaxTurns = 80

// Config assembles an Agent. Zero values get sensible defaults.
type Config struct {
	Model        ai.Model
	Tools        *tools.Registry
	Hooks        Hooks
	SystemPrompt string
	StreamOpts   ai.StreamOptions
	// MaxTurns: <0 = unlimited, 0 = DefaultMaxTurns.
	MaxTurns int
	// SequentialTools forces one-at-a-time tool execution for every batch.
	SequentialTools bool
	// SteerDrain selects how many queued steering messages one [INJECT]
	// consumes (DrainAll default).
	SteerDrain DrainMode
	// Stream overrides the provider lookup (tests inject scripted streams).
	Stream StreamFunc
	Log    *slog.Logger
	// History seeds the conversation with messages from a resumed session;
	// they are context for future runs, never re-emitted or re-persisted.
	History []AgentMessage
}

// Agent owns the conversation state and run lifecycle around runLoop.
type Agent struct {
	mu       sync.Mutex
	running  bool
	cancel   context.CancelFunc
	messages []AgentMessage

	cfg      Config
	maxTurns int
	steering *boundedQueue[AgentMessage]
	followUp *boundedQueue[AgentMessage]
	bus      *eventBus
	log      *slog.Logger
}

// New builds an Agent from cfg.
func New(cfg Config) *Agent {
	log := cfg.Log
	if log == nil {
		log = slog.Default()
	}
	if cfg.Tools == nil {
		cfg.Tools = tools.NewRegistry()
	}
	if cfg.Stream == nil {
		cfg.Stream = registryStream
	}
	maxTurns := cfg.MaxTurns
	switch {
	case maxTurns < 0:
		maxTurns = 0
	case maxTurns == 0:
		maxTurns = DefaultMaxTurns
	}
	return &Agent{
		cfg:      cfg,
		maxTurns: maxTurns,
		messages: append([]AgentMessage(nil), cfg.History...),
		steering: newBoundedQueue[AgentMessage](defaultQueueCap, cfg.SteerDrain),
		followUp: newBoundedQueue[AgentMessage](defaultQueueCap, DrainAll),
		bus:      newEventBus(log),
		log:      log,
	}
}

// registryStream resolves the provider from the global registry; resolution
// failure becomes an error stream so the loop's error path handles it.
func registryStream(ctx context.Context, model ai.Model, c ai.Context, opts ai.StreamOptions) <-chan ai.StreamEvent {
	ch, err := ai.Stream(ctx, model, c, opts)
	if err != nil {
		out := make(chan ai.StreamEvent, 1)
		out <- ai.EventError{Reason: ai.StopReasonError, Message: ai.AssistantMessage{
			API: model.API, Provider: model.Provider, Model: model.ID,
			StopReason: ai.StopReasonError, ErrorMessage: err.Error(),
			Timestamp: time.Now().UnixMilli(),
		}}
		close(out)
		return out
	}
	return ch
}

// Subscribe registers an event consumer; the returned func unsubscribes.
func (a *Agent) Subscribe() (<-chan Event, func()) { return a.bus.Subscribe() }

// Notify publishes a harness-level event (compaction progress, notices) to
// every subscriber alongside the loop's own events.
func (a *Agent) Notify(ev Event) { a.bus.Publish(ev) }

// Messages returns a copy of the conversation so far.
func (a *Agent) Messages() []AgentMessage {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]AgentMessage(nil), a.messages...)
}

// Prompt starts a run with the given user text. It blocks until the run
// ends; concurrent calls fail with ErrBusy.
func (a *Agent) Prompt(ctx context.Context, text string) error {
	msg := ModelMessage{Msg: ai.UserMessage{
		Content:   []ai.Content{ai.TextContent{Text: text}},
		Timestamp: time.Now().UnixMilli(),
	}}
	return a.run(ctx, []AgentMessage{msg})
}

// PromptMessages starts a run with pre-built messages.
func (a *Agent) PromptMessages(ctx context.Context, msgs ...AgentMessage) error {
	return a.run(ctx, msgs)
}

// Continue resumes after a stop (e.g. MaxTurns) without new user input.
func (a *Agent) Continue(ctx context.Context) error {
	return a.run(ctx, nil)
}

// Steer queues a message injected before the next turn of the active run.
func (a *Agent) Steer(msg AgentMessage) error { return a.steering.Push(msg) }

// FollowUp queues a message that extends the run when it would stop.
func (a *Agent) FollowUp(msg AgentMessage) error { return a.followUp.Push(msg) }

// Abort cancels the active run, if any.
func (a *Agent) Abort() {
	a.mu.Lock()
	cancel := a.cancel
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// run guards single-run execution and survives loop bugs: a runLoop panic
// becomes a synthetic error message + EvAgentEnd instead of a dead process.
func (a *Agent) run(ctx context.Context, initial []AgentMessage) error {
	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return ErrBusy
	}
	runCtx, cancel := context.WithCancel(ctx)
	a.running = true
	a.cancel = cancel
	history := append([]AgentMessage(nil), a.messages...)
	a.mu.Unlock()

	deps := &loopDeps{
		Stream:       a.cfg.Stream,
		Tools:        a.cfg.Tools,
		Hooks:        a.cfg.Hooks,
		Sink:         a.bus.Publish,
		Log:          a.log,
		Model:        a.cfg.Model,
		Opts:         a.cfg.StreamOpts,
		SystemPrompt: a.cfg.SystemPrompt,
		MaxTurns:     a.maxTurns,
		Sequential:   a.cfg.SequentialTools,
		Steering:     a.steering,
		FollowUp:     a.followUp,
	}

	newMsgs := a.safeRunLoop(runCtx, deps, history, initial)

	a.mu.Lock()
	a.messages = append(a.messages, newMsgs...)
	a.running = false
	a.cancel = nil
	a.mu.Unlock()
	cancel()
	return nil
}

// safeRunLoop converts a panicking loop into an error-terminated run.
func (a *Agent) safeRunLoop(ctx context.Context, deps *loopDeps, history, initial []AgentMessage) (newMsgs []AgentMessage) {
	defer func() {
		if r := recover(); r != nil {
			buf := make([]byte, 16*1024)
			stack := string(buf[:runtime.Stack(buf, false)])
			a.log.Error("agent: run loop panicked", "panic", r, "stack", stack)
			synthetic := ModelMessage{Msg: ai.AssistantMessage{
				API:          a.cfg.Model.API,
				Provider:     a.cfg.Model.Provider,
				Model:        a.cfg.Model.ID,
				StopReason:   ai.StopReasonError,
				ErrorMessage: fmt.Sprintf("internal error: %v", r),
				Timestamp:    time.Now().UnixMilli(),
			}}
			newMsgs = append(newMsgs, synthetic)
			a.bus.Publish(EvMessageEnd{Msg: synthetic})
			a.bus.Publish(EvAgentEnd{Reason: EndError, Messages: newMsgs})
		}
	}()
	newMsgs, _ = runLoop(ctx, deps, history, initial)
	return newMsgs
}
