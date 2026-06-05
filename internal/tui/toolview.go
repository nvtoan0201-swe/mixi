package tui

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/user/mixi-agent/internal/ai"
)

const previewLines = 8

var (
	toolHeadStyle = lipgloss.NewStyle().Bold(true)
	toolBodyStyle = lipgloss.NewStyle().Faint(true)
	okStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	failStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	diffAddStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	diffDelStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	diffHunkStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("14"))
)

type toolState int

const (
	toolRunning toolState = iota
	toolOK
	toolFail
)

// toolBlock is one tool-call card: spinner while running, ✓/✗ when done,
// body folded to a short preview unless ctrl+o expands all cards.
type toolBlock struct {
	call    ai.ToolCall
	state   toolState
	partial string // streamed output while running
	body    string // final preview text (diff for edit/write)
	isDiff  bool
	started time.Time
	elapsed time.Duration
}

func (b *toolBlock) id() string { return "tool:" + b.call.ID }

// finish ingests the result: edit/write cards prefer the structured diff
// from result details, everything else shows the text content.
func (b *toolBlock) finish(res ai.ToolResultMessage) {
	b.elapsed = time.Since(b.started).Round(100 * time.Millisecond)
	if res.IsError {
		b.state = toolFail
	} else {
		b.state = toolOK
	}
	if d := detailsDiff(res.Details); d != "" && !res.IsError {
		b.body, b.isDiff = d, true
		return
	}
	b.body = resultText(res)
}

func detailsDiff(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var d struct {
		Diff string `json:"diff"`
	}
	if json.Unmarshal(raw, &d) != nil {
		return ""
	}
	return strings.TrimRight(d.Diff, "\n")
}

func resultText(res ai.ToolResultMessage) string {
	var sb strings.Builder
	for _, c := range res.Content {
		if t, ok := c.(ai.TextContent); ok {
			sb.WriteString(t.Text)
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}

func (b *toolBlock) render(rc renderCtx) string {
	head := toolHeadStyle.Render("⚙ " + b.call.Name + headTarget(b.call))
	switch b.state {
	case toolRunning:
		el := ""
		if !b.started.IsZero() {
			el = fmt.Sprintf(" %ds", int(time.Since(b.started).Seconds()))
		}
		head = rc.spinner + " " + head + el
	case toolOK:
		head = okStyle.Render("✓") + " " + head
	case toolFail:
		head = failStyle.Render("✗") + " " + head
	}
	body := b.body
	if b.state == toolRunning {
		body = b.partial
	}
	body = foldBody(body, rc.expandTools)
	if body == "" {
		return head
	}
	if b.isDiff {
		return head + "\n" + colorizeDiff(body)
	}
	return head + "\n" + toolBodyStyle.Render(body)
}

// headTarget summarizes the call's main argument for the card header.
func headTarget(call ai.ToolCall) string {
	var a struct {
		Path    string `json:"path"`
		Command string `json:"command"`
		Pattern string `json:"pattern"`
	}
	_ = json.Unmarshal(call.Args, &a)
	switch {
	case a.Command != "":
		return " " + firstLine(a.Command)
	case a.Path != "":
		return " " + a.Path
	case a.Pattern != "":
		return " " + a.Pattern
	}
	return ""
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i] + " …"
	}
	return s
}

// foldBody trims the card body to previewLines unless expanded.
func foldBody(body string, expand bool) string {
	body = strings.TrimRight(body, "\n")
	if body == "" || expand {
		return body
	}
	lines := strings.Split(body, "\n")
	if len(lines) <= previewLines {
		return body
	}
	folded := append(lines[:previewLines:previewLines],
		fmt.Sprintf("… (+%d lines, ctrl+o)", len(lines)-previewLines))
	return strings.Join(folded, "\n")
}

// colorizeDiff styles unified-diff lines: additions green, deletions red,
// hunk headers cyan.
func colorizeDiff(diff string) string {
	lines := strings.Split(diff, "\n")
	for i, l := range lines {
		switch {
		case strings.HasPrefix(l, "+"):
			lines[i] = diffAddStyle.Render(l)
		case strings.HasPrefix(l, "-"):
			lines[i] = diffDelStyle.Render(l)
		case strings.HasPrefix(l, "@@"):
			lines[i] = diffHunkStyle.Render(l)
		default:
			lines[i] = toolBodyStyle.Render(l)
		}
	}
	return strings.Join(lines, "\n")
}
