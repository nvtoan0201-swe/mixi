package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/user/mixi-agent/internal/ai"
)

var (
	userStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("12")).Bold(true)
	thinkingStyle = lipgloss.NewStyle().Faint(true).Italic(true)
	noticeStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
	errorStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
)

// renderCtx carries per-frame rendering inputs shared by all blocks.
type renderCtx struct {
	width          int
	expandThinking bool
	expandTools    bool
	spinner        string
	markdown       func(string) string // glamour, identity fallback
}

// segKind separates the content lanes of one assistant message.
type segKind int

const (
	segText segKind = iota
	segThinking
)

type seg struct {
	kind segKind
	text string
}

// msgBlock renders one user or assistant message. While streaming, text is
// shown raw (markdown reflow mid-stream churns the viewport); the finished
// message is rendered once via glamour and cached per width.
type msgBlock struct {
	blockID string
	role    ai.Role
	segs    []seg
	done    bool
	errText string

	cached      string
	cachedWidth int
	cachedThink bool
}

func (b *msgBlock) id() string { return b.blockID }

// appendDelta routes a streaming fragment into the current segment lane.
func (b *msgBlock) appendDelta(kind segKind, delta string) {
	if n := len(b.segs); n > 0 && b.segs[n-1].kind == kind {
		b.segs[n-1].text += delta
		return
	}
	b.segs = append(b.segs, seg{kind: kind, text: delta})
}

func (b *msgBlock) render(rc renderCtx) string {
	if b.done && b.cached != "" && b.cachedWidth == rc.width && b.cachedThink == rc.expandThinking {
		return b.cached
	}
	out := b.renderSegs(rc)
	if b.done {
		b.cached, b.cachedWidth, b.cachedThink = out, rc.width, rc.expandThinking
	}
	return out
}

func (b *msgBlock) renderSegs(rc renderCtx) string {
	var parts []string
	for _, s := range b.segs {
		switch s.kind {
		case segThinking:
			if v := renderThinking(s.text, rc); v != "" {
				parts = append(parts, v)
			}
		default:
			parts = append(parts, b.renderText(s.text, rc))
		}
	}
	if b.errText != "" {
		parts = append(parts, errorStyle.Render("✗ "+b.errText))
	}
	return strings.Join(parts, "\n")
}

func (b *msgBlock) renderText(text string, rc renderCtx) string {
	if b.role == ai.RoleUser {
		return userStyle.Render("> ") + strings.TrimRight(text, "\n")
	}
	if !b.done {
		return strings.TrimRight(text, "\n") // raw while streaming
	}
	return strings.TrimRight(rc.markdown(text), "\n")
}

// renderThinking folds a thinking block to its first line plus a counter;
// ctrl+t expands. Styling is dim italic in both states.
func renderThinking(text string, rc renderCtx) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	lines := strings.Split(text, "\n")
	if rc.expandThinking || len(lines) == 1 {
		return thinkingStyle.Render(text)
	}
	return thinkingStyle.Render(fmt.Sprintf("%s (+%d lines, ctrl+t)", lines[0], len(lines)-1))
}

// noticeBlock renders harness notices: retries, compaction, command output.
type noticeBlock struct {
	blockID string
	text    string
	isErr   bool
}

func (b *noticeBlock) id() string { return b.blockID }

func (b *noticeBlock) render(renderCtx) string {
	if b.isErr {
		return errorStyle.Render(b.text)
	}
	return noticeStyle.Render(b.text)
}
