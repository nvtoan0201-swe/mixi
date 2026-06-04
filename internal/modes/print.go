package modes

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/user/mixi-agent/internal/agent"
	"github.com/user/mixi-agent/internal/ai"
)

// Exit codes per the print-mode contract. 130 = 128+SIGINT, the shell
// convention for interrupted commands.
const (
	ExitOK     = 0
	ExitRunErr = 1
	ExitUsage  = 2
	ExitSIGINT = 130
)

// PrintOptions configures one print-mode invocation.
type PrintOptions struct {
	Prompt     string
	Messages   []string // follow-up prompts, queued before the run starts
	JSON       bool     // JSONL event stream instead of final text
	PrintStats bool     // usage summary on errOut after the run
}

// PrintDeps wires the run: an assembled agent (whose hooks own persistence
// and context management) and the output writers.
type PrintDeps struct {
	Agent  *agent.Agent
	Out    io.Writer
	ErrOut io.Writer
	Log    *slog.Logger
}

// RunPrint executes one headless run and returns the process exit code.
// Message persistence happens synchronously inside the agent's hooks, so a
// crash mid-run loses at most the in-flight turn.
func RunPrint(ctx context.Context, d PrintDeps, opts PrintOptions) int {
	var sink EventSink
	if opts.JSON {
		sink = NewJSONSink(d.Out)
	} else {
		sink = NewTextSink(d.Out, d.ErrOut)
	}

	events, unsubscribe := d.Agent.Subscribe()
	defer unsubscribe()

	var stats usageStats
	endReason := make(chan agent.EndReason, 1)
	go func() {
		for ev := range events {
			stats.observe(ev)
			sink.Emit(ev)
			if end, ok := ev.(agent.EvAgentEnd); ok {
				endReason <- end.Reason
				return
			}
		}
		// Channel closed without EvAgentEnd: subscriber was disconnected.
		endReason <- agent.EndError
	}()

	for _, m := range opts.Messages {
		if err := d.Agent.FollowUp(userMessage(m)); err != nil {
			fmt.Fprintf(d.ErrOut, "mixi: queue follow-up: %v\n", err)
			return ExitUsage
		}
	}
	if err := d.Agent.Prompt(ctx, opts.Prompt); err != nil {
		fmt.Fprintf(d.ErrOut, "mixi: %v\n", err)
		return ExitRunErr
	}

	reason := <-endReason
	if opts.PrintStats {
		stats.print(d.ErrOut)
	}
	switch reason {
	case agent.EndError:
		return ExitRunErr
	case agent.EndAborted:
		return ExitSIGINT
	default: // done, maxTurns — the run stopped cleanly
		return ExitOK
	}
}

func userMessage(text string) agent.AgentMessage {
	return agent.ModelMessage{Msg: ai.UserMessage{
		Content:   []ai.Content{ai.TextContent{Text: text}},
		Timestamp: time.Now().UnixMilli(),
	}}
}

// usageStats aggregates per-run token usage from assistant messages.
type usageStats struct {
	turns int
	usage ai.Usage
}

func (s *usageStats) observe(ev agent.Event) {
	end, ok := ev.(agent.EvMessageEnd)
	if !ok {
		return
	}
	mm, ok := end.Msg.(agent.ModelMessage)
	if !ok {
		return
	}
	am, ok := mm.Msg.(ai.AssistantMessage)
	if !ok {
		return
	}
	s.turns++
	s.usage.Input += am.Usage.Input
	s.usage.Output += am.Usage.Output
	s.usage.CacheRead += am.Usage.CacheRead
	s.usage.CacheWrite += am.Usage.CacheWrite
	s.usage.Total += am.Usage.Total
	s.usage.Cost.Total += am.Usage.Cost.Total
}

func (s *usageStats) print(w io.Writer) {
	fmt.Fprintf(w, "turns=%d tokens: input=%d output=%d cacheRead=%d cacheWrite=%d total=%d cost=$%.4f\n",
		s.turns, s.usage.Input, s.usage.Output, s.usage.CacheRead, s.usage.CacheWrite,
		s.usage.Total, s.usage.Cost.Total)
}
