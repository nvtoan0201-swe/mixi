package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/user/mixi-agent/internal/tools"
)

// projectContextFiles are read from the working directory, in this order,
// and embedded in the system prompt so project instructions survive
// compaction by construction.
var projectContextFiles = []string{"AGENTS.md", "CLAUDE.md", ".mixi/context.md"}

const sysPromptHeader = `You are an expert coding assistant operating inside mixi, a coding agent harness. You help the user with software engineering tasks: writing code, fixing bugs, refactoring, and answering questions about the codebase.`

const sysPromptGuidelines = `Guidelines:
- Prefer reading files before editing them; never guess at file contents.
- Make focused, minimal changes; match the surrounding code style.
- Use tools to verify your work (run tests, re-read edited files) when possible.
- When a tool call fails, read the error and correct your arguments rather than repeating the same call.
- Be concise in your replies; the user sees your text output in a terminal.`

// SysPromptOptions parameterizes BuildSystemPrompt.
type SysPromptOptions struct {
	Tools []tools.Tool
	CWD   string    // empty → os.Getwd()
	Now   time.Time // zero → time.Now()
}

// BuildSystemPrompt assembles the system prompt: header, one-line tool
// snippets, guidelines, <project_context> file embeds, and — last, where
// models attend most — the current date and working directory.
func BuildSystemPrompt(opts SysPromptOptions) string {
	cwd := opts.CWD
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}

	var b strings.Builder
	b.WriteString(sysPromptHeader)
	b.WriteString("\n\n")

	if len(opts.Tools) > 0 {
		b.WriteString("Available tools:\n")
		for _, t := range opts.Tools {
			b.WriteString(fmt.Sprintf("- %s: %s\n", t.Name(), firstLine(t.Description())))
		}
		b.WriteString("\n")
	}

	b.WriteString(sysPromptGuidelines)
	b.WriteString("\n")

	if pc := buildProjectContext(cwd); pc != "" {
		b.WriteString("\n")
		b.WriteString(pc)
	}

	b.WriteString(fmt.Sprintf("\nCurrent date: %s\n", now.Format("2006-01-02")))
	b.WriteString(fmt.Sprintf("Current working directory: %s\n", cwd))
	return b.String()
}

// buildProjectContext embeds any present project-instruction files in
// <project_context>/<project_instructions path=...> wrappers.
func buildProjectContext(cwd string) string {
	var sections []string
	for _, name := range projectContextFiles {
		path := filepath.Join(cwd, name)
		data, err := os.ReadFile(path)
		if err != nil || len(data) == 0 {
			continue
		}
		sections = append(sections, fmt.Sprintf("<project_instructions path=%q>\n%s\n</project_instructions>", name, strings.TrimSpace(string(data))))
	}
	if len(sections) == 0 {
		return ""
	}
	return "<project_context>\n" + strings.Join(sections, "\n") + "\n</project_context>\n"
}

// firstLine clips a tool description to its first line for the prompt.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
