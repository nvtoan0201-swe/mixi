package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"

	"github.com/user/mixi-agent/internal/agent"
	"github.com/user/mixi-agent/internal/ai"
)

// block is one transcript component, keyed by message/tool id.
type block interface {
	id() string
	render(rc renderCtx) string
}

// transcriptModel owns the scrollable conversation view. It sticks to the
// bottom while the user is at the bottom; scrolling up pins the view until
// they return.
type transcriptModel struct {
	vp     viewport.Model
	blocks []block
	byID   map[string]block
	live   *msgBlock // assistant message currently streaming
	spin   spinner.Model

	expandThinking bool
	expandTools    bool
	width          int
	glam           *glamour.TermRenderer
	nextID         int
	anyRunning     bool // a tool card is active → spinner ticks
}

func newTranscript() transcriptModel {
	sp := spinner.New(spinner.WithSpinner(spinner.MiniDot))
	return transcriptModel{
		vp:   viewport.New(0, 0),
		byID: map[string]block{},
		spin: sp,
	}
}

func (t *transcriptModel) setSize(w, h int) {
	t.vp.Width, t.vp.Height = w, h
	if w != t.width {
		t.width = w
		t.glam = nil // re-created lazily at the new width
	}
	t.refresh()
}

// markdown renders text through glamour; on failure the raw text stands.
func (t *transcriptModel) markdown(text string) string {
	if t.glam == nil {
		g, err := glamour.NewTermRenderer(
			glamour.WithAutoStyle(),
			glamour.WithWordWrap(max(20, t.width-2)),
		)
		if err != nil {
			return text
		}
		t.glam = g
	}
	out, err := t.glam.Render(text)
	if err != nil {
		return text
	}
	return out
}

func (t *transcriptModel) renderCtx() renderCtx {
	return renderCtx{
		width:          t.width,
		expandThinking: t.expandThinking,
		expandTools:    t.expandTools,
		spinner:        t.spin.View(),
		markdown:       t.markdown,
	}
}

func (t *transcriptModel) add(b block) {
	t.blocks = append(t.blocks, b)
	t.byID[b.id()] = b
	t.refresh()
}

func (t *transcriptModel) notice(text string, isErr bool) {
	t.nextID++
	t.add(&noticeBlock{blockID: fmt.Sprintf("notice:%d", t.nextID), text: text, isErr: isErr})
}

// refresh re-renders all blocks into the viewport, preserving stickiness.
func (t *transcriptModel) refresh() {
	atBottom := t.vp.AtBottom()
	parts := make([]string, 0, len(t.blocks))
	rc := t.renderCtx()
	for _, b := range t.blocks {
		parts = append(parts, b.render(rc))
	}
	t.vp.SetContent(strings.Join(parts, "\n\n"))
	if atBottom {
		t.vp.GotoBottom()
	}
}

// apply routes one agent event into the transcript. Unhandled event types
// fall through silently — the status bar refreshes on every event anyway.
func (t *transcriptModel) apply(ev agent.Event) {
	switch ev := ev.(type) {
	case agent.EvMessageStart:
		t.openMessage(ev.Msg)
	case agent.EvMessageUpdate:
		t.applyStream(ev.StreamEvent)
	case agent.EvMessageEnd:
		t.closeMessage(ev.Msg)
	case agent.EvToolStart:
		t.anyRunning = true
		t.add(&toolBlock{call: ev.Call, state: toolRunning, started: time.Now()})
	case agent.EvToolUpdate:
		if tb, ok := t.byID["tool:"+ev.CallID].(*toolBlock); ok {
			tb.partial = ev.Partial
			t.refresh()
		}
	case agent.EvToolEnd:
		if tb, ok := t.byID["tool:"+ev.CallID].(*toolBlock); ok {
			tb.finish(ev.Result)
			t.refresh()
		}
		t.anyRunning = false
	case agent.EvRetryStart:
		t.notice(fmt.Sprintf("Retrying (attempt %d/%d in %s): %s", ev.Attempt, ev.Max, ev.Delay, ev.Err), false)
	case agent.EvCompactionStart:
		t.notice("Compacting context…", false)
	case agent.EvCompactionEnd:
		t.notice(fmt.Sprintf("Compacted (%d tokens before)", ev.TokensBefore), false)
	case agent.EvNotice:
		t.notice(ev.Text, false)
	}
}

// openMessage starts a transcript entry for a message announced by the loop.
// Assistant messages stream into a live block; user-visible injected
// messages render immediately.
func (t *transcriptModel) openMessage(m agent.AgentMessage) {
	t.nextID++
	id := fmt.Sprintf("msg:%d", t.nextID)
	switch v := m.(type) {
	case agent.ModelMessage:
		switch mm := v.Msg.(type) {
		case ai.AssistantMessage:
			t.live = &msgBlock{blockID: id, role: ai.RoleAssistant}
			t.add(t.live)
		case ai.UserMessage:
			b := &msgBlock{blockID: id, role: ai.RoleUser, done: true}
			b.appendDelta(segText, userMessageText(mm))
			t.add(b)
		}
	case agent.BashExecution:
		t.notice(fmt.Sprintf("! %s (exit %d)\n%s", v.Command, v.ExitCode, v.Output), v.ExitCode != 0)
	case agent.CompactionSummary, agent.BranchSummary:
		t.notice("Summary installed from a previous context.", false)
	case agent.CustomMessage:
		t.notice(v.Content, false)
	}
}

// applyStream appends provider deltas to the live assistant block.
func (t *transcriptModel) applyStream(ev ai.StreamEvent) {
	if t.live == nil {
		return
	}
	switch ev := ev.(type) {
	case ai.EventTextDelta:
		t.live.appendDelta(segText, ev.Delta)
	case ai.EventThinkingDelta:
		t.live.appendDelta(segThinking, ev.Delta)
	default:
		return // tool-call deltas render as cards on EvToolStart
	}
	t.refresh()
}

// closeMessage finalizes the live block (markdown render now caches).
func (t *transcriptModel) closeMessage(m agent.AgentMessage) {
	mm, ok := m.(agent.ModelMessage)
	if !ok || t.live == nil {
		return
	}
	am, ok := mm.Msg.(ai.AssistantMessage)
	if !ok {
		return
	}
	t.live.done = true
	if am.StopReason == ai.StopReasonError || am.StopReason == ai.StopReasonAborted {
		t.live.errText = am.ErrorMessage
	}
	t.live = nil
	t.refresh()
}

// tick advances the spinner while any tool card is running.
func (t *transcriptModel) tick(msg tea.Msg) tea.Cmd {
	sp, cmd := t.spin.Update(msg)
	t.spin = sp
	if t.anyRunning {
		t.refresh()
	}
	return cmd
}

func userMessageText(m ai.UserMessage) string {
	var sb strings.Builder
	for _, c := range m.Content {
		if tc, ok := c.(ai.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	return sb.String()
}
