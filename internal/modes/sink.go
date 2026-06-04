// Package modes hosts the headless run modes (print now; RPC and replay in
// later phases) and the EventSink seam every mode renders agent output
// through.
package modes

import (
	"fmt"
	"io"

	"github.com/user/mixi-agent/internal/agent"
	"github.com/user/mixi-agent/internal/ai"
)

// EventSink receives every agent event of a run. Implementations must be
// fast or internally buffered; the agent's fan-out drops render-only events
// for slow consumers and disconnects them on blocked lifecycle events.
type EventSink interface {
	Emit(ev agent.Event)
}

// TextSink renders a run as plain text: the final assistant text of the run
// goes to out when the run ends. Errors are reported on errOut so stdout
// stays clean for piping.
type TextSink struct {
	out    io.Writer
	errOut io.Writer
}

func NewTextSink(out, errOut io.Writer) *TextSink {
	return &TextSink{out: out, errOut: errOut}
}

func (s *TextSink) Emit(ev agent.Event) {
	end, ok := ev.(agent.EvAgentEnd)
	if !ok {
		return
	}
	if text := finalAssistantText(end.Messages); text != "" {
		fmt.Fprintln(s.out, text)
	}
	if errMsg := lastErrorMessage(end.Messages); errMsg != "" {
		fmt.Fprintf(s.errOut, "mixi: run failed: %s\n", errMsg)
	}
}

// finalAssistantText extracts the text blocks of the last assistant message
// that carries any.
func finalAssistantText(msgs []agent.AgentMessage) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		mm, ok := msgs[i].(agent.ModelMessage)
		if !ok {
			continue
		}
		am, ok := mm.Msg.(ai.AssistantMessage)
		if !ok {
			continue
		}
		text := ""
		for _, c := range am.Content {
			if tc, ok := c.(ai.TextContent); ok {
				if text != "" {
					text += "\n"
				}
				text += tc.Text
			}
		}
		if text != "" {
			return text
		}
	}
	return ""
}

// lastErrorMessage returns the error text of the run's last failed
// assistant turn, if any.
func lastErrorMessage(msgs []agent.AgentMessage) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if mm, ok := msgs[i].(agent.ModelMessage); ok {
			if am, ok := mm.Msg.(ai.AssistantMessage); ok {
				return am.ErrorMessage
			}
		}
	}
	return ""
}
