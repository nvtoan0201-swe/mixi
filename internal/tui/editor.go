package tui

import (
	"os"
	"os/exec"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
)

const historyCap = 50

// editorDoneMsg returns control after the external $EDITOR exits.
type editorDoneMsg struct {
	path string
	err  error
}

// editorModel is the prompt input: a textarea with input history and
// slash-command autocomplete.
type editorModel struct {
	ta      textarea.Model
	history []string
	histIdx int // == len(history) when not browsing
	draft   string
}

func newEditor() editorModel {
	ta := textarea.New()
	ta.Placeholder = "Prompt (enter sends, alt+enter queues, / for commands)"
	ta.SetHeight(3)
	ta.ShowLineNumbers = false
	ta.Focus()
	return editorModel{ta: ta, histIdx: 0}
}

func (e *editorModel) setWidth(w int) { e.ta.SetWidth(w) }

func (e *editorModel) value() string { return strings.TrimSpace(e.ta.Value()) }

func (e *editorModel) clear() {
	e.ta.Reset()
	e.histIdx = len(e.history)
}

// remember stores a submitted prompt in the history ring.
func (e *editorModel) remember(text string) {
	if text == "" {
		return
	}
	e.history = append(e.history, text)
	if len(e.history) > historyCap {
		e.history = e.history[len(e.history)-historyCap:]
	}
	e.histIdx = len(e.history)
}

// histPrev/histNext browse history when the input is empty or already
// browsing; the in-progress draft is preserved.
func (e *editorModel) histPrev() bool {
	if len(e.history) == 0 || e.histIdx == 0 {
		return false
	}
	if e.histIdx == len(e.history) {
		e.draft = e.ta.Value()
	}
	e.histIdx--
	e.ta.SetValue(e.history[e.histIdx])
	e.ta.CursorEnd()
	return true
}

func (e *editorModel) histNext() bool {
	if e.histIdx >= len(e.history) {
		return false
	}
	e.histIdx++
	if e.histIdx == len(e.history) {
		e.ta.SetValue(e.draft)
	} else {
		e.ta.SetValue(e.history[e.histIdx])
	}
	e.ta.CursorEnd()
	return true
}

// browsing reports whether history navigation should own up/down keys.
func (e *editorModel) browsing() bool {
	return e.ta.Value() == "" || e.histIdx < len(e.history)
}

// suggestions returns slash commands matching the current "/prefix" input.
func (e *editorModel) suggestions(table []commandSpec) []commandSpec {
	v := e.ta.Value()
	if !strings.HasPrefix(v, "/") || strings.ContainsAny(v, " \n") {
		return nil
	}
	var out []commandSpec
	for _, c := range table {
		if strings.HasPrefix(c.name, v) {
			out = append(out, c)
		}
	}
	if len(out) == 1 && out[0].name == v {
		return nil // already complete
	}
	return out
}

// complete fills in the first matching command.
func (e *editorModel) complete(table []commandSpec) bool {
	s := e.suggestions(table)
	if len(s) == 0 {
		return false
	}
	e.ta.SetValue(s[0].name + " ")
	e.ta.CursorEnd()
	return true
}

func (e *editorModel) update(msg tea.Msg) tea.Cmd {
	ta, cmd := e.ta.Update(msg)
	e.ta = ta
	if e.ta.Value() != "" && e.histIdx == len(e.history) {
		e.draft = "" // typing fresh input invalidates the saved draft
	}
	return cmd
}

func (e *editorModel) view() string { return e.ta.View() }

// openExternalEditor suspends the TUI and runs $EDITOR on a temp file seeded
// with the current input; the result lands back in the textarea.
func (e *editorModel) openExternalEditor() tea.Cmd {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		return func() tea.Msg { return editorDoneMsg{err: errNoEditor} }
	}
	f, err := os.CreateTemp("", "mixi-prompt-*.md")
	if err != nil {
		return func() tea.Msg { return editorDoneMsg{err: err} }
	}
	path := f.Name()
	_, werr := f.WriteString(e.ta.Value())
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		os.Remove(path)
		return func() tea.Msg { return editorDoneMsg{err: werr} }
	}
	cmd := exec.Command(editor, path)
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		return editorDoneMsg{path: path, err: err}
	})
}

// finishExternalEditor ingests the edited file and cleans up.
func (e *editorModel) finishExternalEditor(msg editorDoneMsg) error {
	if msg.path != "" {
		defer os.Remove(msg.path)
	}
	if msg.err != nil {
		return msg.err
	}
	raw, err := os.ReadFile(msg.path)
	if err != nil {
		return err
	}
	e.ta.SetValue(strings.TrimRight(string(raw), "\n"))
	e.ta.CursorEnd()
	return nil
}

type noEditorError struct{}

func (noEditorError) Error() string { return "$EDITOR is not set" }

var errNoEditor = noEditorError{}
