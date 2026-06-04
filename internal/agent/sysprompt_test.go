package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBuildSystemPromptOrdering(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("Always run tests."), 0o644)

	reg := registryOf(t,
		scriptTool{name: "read"},
		scriptTool{name: "bash"},
	)
	now := time.Date(2026, 6, 4, 10, 0, 0, 0, time.UTC)
	p := BuildSystemPrompt(SysPromptOptions{Tools: reg.All(), CWD: dir, Now: now})

	for _, want := range []string{
		"- read: test tool read",
		"- bash: test tool bash",
		"Guidelines:",
		`<project_instructions path="AGENTS.md">`,
		"Always run tests.",
		"Current date: 2026-06-04",
		"Current working directory: " + dir,
	} {
		if !strings.Contains(p, want) {
			t.Fatalf("prompt missing %q:\n%s", want, p)
		}
	}
	// Date and cwd must come last, after project context.
	if strings.Index(p, "Current date:") < strings.Index(p, "<project_context>") {
		t.Fatal("date must follow project context")
	}
	if strings.Index(p, "Current working directory:") < strings.Index(p, "Current date:") {
		t.Fatal("cwd must be the final line block")
	}
}

func TestBuildSystemPromptSkipsMissingProjectFiles(t *testing.T) {
	dir := t.TempDir()
	p := BuildSystemPrompt(SysPromptOptions{CWD: dir, Now: time.Now()})
	if strings.Contains(p, "<project_context>") {
		t.Fatal("empty project context should be omitted")
	}
}

func TestBuildSystemPromptReadsNestedContextFile(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".mixi"), 0o755)
	os.WriteFile(filepath.Join(dir, ".mixi", "context.md"), []byte("project notes"), 0o644)
	p := BuildSystemPrompt(SysPromptOptions{CWD: dir, Now: time.Now()})
	if !strings.Contains(p, `<project_instructions path=".mixi/context.md">`) || !strings.Contains(p, "project notes") {
		t.Fatalf("nested context file not embedded:\n%s", p)
	}
}

func TestBuildSystemPromptClipsToolDescriptionToFirstLine(t *testing.T) {
	if got := firstLine("line one\nline two"); got != "line one" {
		t.Fatalf("firstLine = %q", got)
	}
	if got := firstLine("single"); got != "single" {
		t.Fatalf("firstLine = %q", got)
	}
}
