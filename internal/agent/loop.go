package agent

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"time"

	"github.com/user/mixi-agent/internal/ai"
	"github.com/user/mixi-agent/internal/tools"
)

// StreamFunc abstracts the provider call so the loop is testable against a
// scripted stream (agenttest) without the global provider registry.
type StreamFunc func(ctx context.Context, model ai.Model, c ai.Context, opts ai.StreamOptions) <-chan ai.StreamEvent

// loopDeps carries everything runLoop touches; the loop itself holds no
// state between calls, which keeps every branch unit-testable.
type loopDeps struct {
	Stream       StreamFunc
	Tools        *tools.Registry
	Hooks        Hooks
	Filters      []ToolCallFilter // run before BeforeToolCall, in order
	Sink         func(Event)      // event fan-out (bus.Publish in production)
	Log          *slog.Logger
	Model        ai.Model
	Opts         ai.StreamOptions
	SystemPrompt string
	MaxTurns     int  // 0 = unlimited
	Sequential   bool // force sequential tool execution
	Steering     *boundedQueue[AgentMessage]
	FollowUp     *boundedQueue[AgentMessage]
	Rand         *rand.Rand    // jitter source; nil → seeded from time
	RetryBase    time.Duration // 0 → production default (tests shrink it)
}

func (d *loopDeps) emit(ev Event) { d.Sink(ev) }

func (d *loopDeps) jitterRand() *rand.Rand {
	if d.Rand == nil {
		d.Rand = rand.New(rand.NewSource(time.Now().UnixNano()))
	}
	return d.Rand
}

// runLoop executes one agent run: the inner loop streams assistant turns and
// dispatches tools while messages or tool calls remain; the outer loop
// restarts the inner loop when follow-up messages arrive at a natural stop.
// It returns every new AgentMessage produced plus the end reason.
func runLoop(ctx context.Context, d *loopDeps, history []AgentMessage, initial []AgentMessage) ([]AgentMessage, EndReason) {
	d.emit(EvAgentStart{})
	msgs := append([]AgentMessage(nil), history...)
	var newMsgs []AgentMessage
	turn := 0
	model := d.Model
	opts := d.Opts

	record := func(m AgentMessage) {
		msgs = append(msgs, m)
		newMsgs = append(newMsgs, m)
		d.Hooks.onMessage(m)
	}
	inject := func(batch []AgentMessage) {
		for _, m := range batch {
			d.emit(EvMessageStart{Msg: m})
			record(m)
			d.emit(EvMessageEnd{Msg: m})
		}
	}
	end := func(reason EndReason) ([]AgentMessage, EndReason) {
		d.emit(EvAgentEnd{Reason: reason, Messages: newMsgs})
		return newMsgs, reason
	}

	pending := initial
	// Continue() passes no new messages: force one turn over the existing
	// context so a MaxTurns-stopped run can resume.
	forceTurn := len(initial) == 0
	for { // outer loop: follow-ups extend the run
		hadToolCalls := forceTurn
		forceTurn = false
		for len(pending) > 0 || hadToolCalls || d.Steering.Len() > 0 {
			// [INJECT] pending prompts/follow-ups, then queued steering.
			inject(pending)
			pending = nil
			inject(d.Steering.Drain())

			// [GUARD] cap runaway tool loops.
			turn++
			if d.MaxTurns > 0 && turn > d.MaxTurns {
				d.emit(EvNotice{Text: fmt.Sprintf("Run stopped: reached the maximum of %d turns. Use Continue to resume.", d.MaxTurns)})
				return end(EndMaxTurns)
			}
			d.emit(EvTurnStart{Turn: turn})

			// [STREAM] one assistant turn, with transient-retry handling.
			asst, ok := streamTurn(ctx, d, msgs, model, opts)
			record(ModelMessage{Msg: asst})
			d.emit(EvMessageEnd{Msg: ModelMessage{Msg: asst}})
			if !ok {
				if asst.StopReason == ai.StopReasonAborted {
					return end(EndAborted)
				}
				return end(EndError)
			}

			// [TOOLS] dispatch any requested calls; results in source order.
			calls := extractToolCalls(asst)
			hadToolCalls = len(calls) > 0
			var toolResults []ai.ToolResultMessage
			if hadToolCalls {
				toolResults = executeToolCalls(ctx, d, calls)
				for _, r := range toolResults {
					record(ModelMessage{Msg: r})
				}
			}

			// [TURN_END]
			d.emit(EvTurnEnd{Turn: turn, Message: asst, ToolResults: toolResults})
			if ctx.Err() != nil {
				return end(EndAborted)
			}

			// [COMPACT?] post-turn context-management trigger; runs while the
			// session is quiescent so a compaction never races a stream.
			d.Hooks.afterTurn(ctx, d.Log, turn)

			// [HOOKS] PrepareNextTurn may swap model/thinking; ShouldStop
			// ends the run cleanly.
			if upd := d.Hooks.prepareNextTurn(turn); upd != nil {
				if upd.Model != nil {
					model = *upd.Model
				}
				if upd.Thinking != nil {
					opts.Thinking = *upd.Thinking
				}
			}
			if d.Hooks.shouldStop(turn, &asst) {
				return end(EndDone)
			}

			// [STEER?] a steer that arrived during tool execution triggers
			// another turn even without tool calls — checked by the loop
			// condition.
		}

		// [FOLLOWUP?] only polled when the agent would otherwise stop.
		fu := d.FollowUp.Drain()
		if len(fu) == 0 {
			return end(EndDone)
		}
		pending = fu
	}
}

// streamTurn runs the per-turn pipeline: TransformContext hook → ToLLM
// projection → provider stream, retrying transient failures with backoff.
// ok=false means the turn ended in error/abort and the run must stop.
// Streams that already emitted content are never retried (re-running would
// duplicate text). A context-overflow error gets one recovery attempt: the
// failed message is dropped and the OnContextOverflow hook may free room
// (compaction) before the request is rebuilt.
func streamTurn(ctx context.Context, d *loopDeps, msgs []AgentMessage, model ai.Model, opts ai.StreamOptions) (ai.AssistantMessage, bool) {
	attempt := 0
	overflowRetried := false
	for {
		transformed := d.Hooks.transformContext(ctx, d.Log, msgs)
		llmCtx := ai.Context{
			SystemPrompt: d.SystemPrompt,
			Messages:     ToLLM(transformed),
			Tools:        d.Tools.Defs(),
		}
		callOpts := opts
		if key := d.Hooks.apiKey(d.Log, model.Provider); key != "" {
			callOpts.APIKey = key
		}

		final, emittedContent := pumpStream(ctx, d, d.Stream(ctx, model, llmCtx, callOpts))
		if final.StopReason != ai.StopReasonError {
			if attempt > 0 && final.StopReason != ai.StopReasonAborted {
				d.emit(EvRetryEnd{OK: true, Attempt: attempt})
			}
			return final, final.StopReason != ai.StopReasonAborted
		}

		class := classifyError(final.ErrorMessage)
		if class == classOverflow && !emittedContent && !overflowRetried {
			overflowRetried = true
			if d.Hooks.onContextOverflow(ctx) {
				// Context was freed; rebuild and retry once. The failed
				// message is local to this call and simply discarded.
				continue
			}
		}
		retryable := class == classRetryable && !emittedContent
		if !retryable || attempt >= retryMaxAttempts {
			if attempt > 0 {
				d.emit(EvRetryEnd{OK: false, Attempt: attempt})
			}
			return final, false
		}
		attempt++
		delay := retryDelay(attempt, final.ErrorMessage, d.jitterRand(), d.RetryBase)
		d.emit(EvRetryStart{Attempt: attempt, Max: retryMaxAttempts, Delay: delay, Err: final.ErrorMessage})
		if err := sleepCtx(ctx, delay); err != nil {
			final.StopReason = ai.StopReasonAborted
			final.ErrorMessage = err.Error()
			return final, false
		}
	}
}

// pumpStream consumes one provider stream, fanning events to subscribers.
// It returns the final message (Done or Error event) and whether any
// content block was opened before termination.
func pumpStream(ctx context.Context, d *loopDeps, ch <-chan ai.StreamEvent) (ai.AssistantMessage, bool) {
	emittedContent := false
	var final ai.AssistantMessage
	got := false
	for ev := range ch {
		switch e := ev.(type) {
		case ai.EventStart:
			d.emit(EvMessageStart{Msg: ModelMessage{Msg: e.Partial}})
		case ai.EventDone:
			final, got = e.Message, true
		case ai.EventError:
			final, got = e.Message, true
		default:
			emittedContent = true
		}
		d.emit(EvMessageUpdate{StreamEvent: ev})
	}
	if !got {
		// Provider closed without a terminal event — contract violation;
		// treat as abort if the ctx died, else as an error turn.
		reason, msg := ai.StopReasonError, "provider stream closed without Done/Error event"
		if ctx.Err() != nil {
			reason, msg = ai.StopReasonAborted, ctx.Err().Error()
		}
		final = ai.AssistantMessage{
			API:          d.Model.API,
			Provider:     d.Model.Provider,
			Model:        d.Model.ID,
			StopReason:   reason,
			ErrorMessage: msg,
			Timestamp:    time.Now().UnixMilli(),
		}
	}
	return final, emittedContent
}

// extractToolCalls pulls the tool-call blocks from an assistant message.
func extractToolCalls(m ai.AssistantMessage) []ai.ToolCall {
	var calls []ai.ToolCall
	for _, c := range m.Content {
		if tc, ok := c.(ai.ToolCall); ok {
			calls = append(calls, tc)
		}
	}
	return calls
}
