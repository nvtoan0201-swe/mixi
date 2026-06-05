package tui

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/user/mixi-agent/internal/agent"
	"github.com/user/mixi-agent/internal/ai"
)

var statusStyle = lipgloss.NewStyle().
	Foreground(lipgloss.Color("0")).
	Background(lipgloss.Color("7")).
	Padding(0, 1)

// statusModel renders `model • thinking • ctx% • $cost • mode • jobs`.
// Token/cost figures anchor on real provider usage from assistant messages.
type statusModel struct {
	model     ai.Model
	thinking  ai.ThinkingLevel
	permMode  string
	cost      float64
	ctxTokens int
	jobs      int
	running   bool
	width     int
	segments  map[string]string // extension footer segments, keyed by ext name
}

// observe folds usage off the event stream: the latest assistant message's
// usage is the current context size; cost accumulates across the session.
func (s *statusModel) observe(ev agent.Event) {
	if st, ok := ev.(agent.EvStatus); ok {
		if s.segments == nil {
			s.segments = map[string]string{}
		}
		if st.Text == "" {
			delete(s.segments, st.Key)
		} else {
			s.segments[st.Key] = st.Text
		}
		return
	}
	end, ok := ev.(agent.EvMessageEnd)
	if !ok {
		return
	}
	mm, ok := end.Msg.(agent.ModelMessage)
	if !ok {
		return
	}
	am, ok := mm.Msg.(ai.AssistantMessage)
	if !ok || am.Usage.Total == 0 {
		return
	}
	s.ctxTokens = am.Usage.Input + am.Usage.CacheRead + am.Usage.CacheWrite + am.Usage.Output
	s.cost += am.Usage.Cost.Total
}

func (s *statusModel) view() string {
	parts := []string{s.model.DisplayName}
	if s.model.DisplayName == "" {
		parts[0] = s.model.ID
	}
	think := string(s.thinking)
	if think == "" {
		think = string(ai.ThinkingOff)
	}
	parts = append(parts, "think:"+think)
	if s.model.ContextWindow > 0 {
		parts = append(parts, fmt.Sprintf("ctx %d%%", s.ctxTokens*100/s.model.ContextWindow))
	}
	parts = append(parts, fmt.Sprintf("$%.4f", s.cost), "mode:"+s.permMode)
	if s.jobs > 0 {
		parts = append(parts, fmt.Sprintf("%d jobs", s.jobs))
	}
	if s.running {
		parts = append(parts, "running")
	}
	for _, key := range sortedKeys(s.segments) {
		parts = append(parts, s.segments[key])
	}
	line := strings.Join(parts, " • ")
	return statusStyle.Width(max(s.width, lipgloss.Width(line)+2)).Render(line)
}

// sortedKeys orders segment keys so the footer is stable across renders.
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
