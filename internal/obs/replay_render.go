package obs

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/user/mixi-agent/internal/agent"
	"github.com/user/mixi-agent/internal/ai"
)

// transcript renders synthesized events as a plain-text conversation — the
// replay analogue of the live print renderer. Write errors latch into err
// and turn further emits into no-ops.
type transcript struct {
	out io.Writer
	err error
}

func (r *transcript) Emit(ev agent.Event) {
	if r.err != nil {
		return
	}
	switch e := ev.(type) {
	case agent.EvMessageEnd:
		r.message(e.Msg)
	case agent.EvToolStart:
		r.printf("⏺ %s %s\n", e.Call.Name, compactJSON(e.Call.Args))
	case agent.EvToolEnd:
		r.toolResult(e.Result)
	case agent.EvNotice:
		r.printf("• %s\n\n", e.Text)
	case agent.EvCompactionEnd:
		r.printf("• compacted (%d tokens before)\n\n", e.TokensBefore)
	}
}

func (r *transcript) message(m agent.AgentMessage) {
	mm, ok := m.(agent.ModelMessage)
	if !ok {
		return
	}
	switch msg := mm.Msg.(type) {
	case ai.UserMessage:
		for _, line := range strings.Split(textOf(msg.Content), "\n") {
			r.printf("> %s\n", line)
		}
		r.printf("\n")
	case ai.AssistantMessage:
		if text := textOf(msg.Content); text != "" {
			r.printf("%s\n\n", text)
		}
	}
}

func (r *transcript) toolResult(res ai.ToolResultMessage) {
	mark := "⎿"
	if res.IsError {
		mark = "✗"
	}
	lines := strings.Split(textOf(res.Content), "\n")
	r.printf("  %s %s\n", mark, lines[0])
	for _, line := range lines[1:] {
		r.printf("    %s\n", line)
	}
	r.printf("\n")
}

func (r *transcript) printf(format string, args ...any) {
	if r.err != nil {
		return
	}
	_, r.err = fmt.Fprintf(r.out, format, args...)
}

// textOf joins a message's text blocks; thinking and images are skipped.
func textOf(content []ai.Content) string {
	var parts []string
	for _, c := range content {
		if tc, ok := c.(ai.TextContent); ok {
			parts = append(parts, tc.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// compactJSON renders tool args one-line; invalid JSON passes through raw.
func compactJSON(raw json.RawMessage) string {
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return string(raw)
	}
	return buf.String()
}
