package perm

import (
	"strings"
	"testing"
)

func TestParseRule(t *testing.T) {
	good := []string{
		"bash(go test*)", "bash(*sudo*)", "read(**/.env*)", "write(./**)",
		"edit(/etc/**)", "mcp__github__get_*", "mcp__jira__create_issue",
	}
	for _, raw := range good {
		if _, err := ParseRule(raw, "test"); err != nil {
			t.Errorf("ParseRule(%q) error: %v", raw, err)
		}
	}
	bad := []string{"", "bash", "bash()", "grep(*)", "rm -rf", "write[./**]"}
	for _, raw := range bad {
		if _, err := ParseRule(raw, "test"); err == nil {
			t.Errorf("ParseRule(%q) succeeded, want error", raw)
		}
	}
}

func TestParseRuleErrorNamesSource(t *testing.T) {
	_, err := ParseRule("nope", "settings deny")
	if err == nil || !contains(err.Error(), "settings deny") {
		t.Fatalf("error %v does not name the source", err)
	}
}

func TestWildcardMatch(t *testing.T) {
	cases := []struct {
		pattern, s string
		want       bool
	}{
		{"go test*", "go test ./...", true},
		{"go test*", "go test", true},
		{"go test*", "go build", false},
		{"*sudo*", "echo a && sudo rm x", true},
		{"*sudo*", "echo sud o", false},
		{"git status", "git status", true},
		{"git status", "git status --short", false},
		{"mcp__github__get_*", "mcp__github__get_issue", true},
		{"mcp__github__get_*", "mcp__github__create_issue", false},
	}
	for _, c := range cases {
		if got := wildcardMatch(c.pattern, c.s); got != c.want {
			t.Errorf("wildcardMatch(%q, %q) = %v, want %v", c.pattern, c.s, got, c.want)
		}
	}
}

func TestPathGlobMatch(t *testing.T) {
	cwd := "/home/u/proj"
	cases := []struct {
		pattern, path string
		want          bool
	}{
		{"./**", "/home/u/proj/src/a.go", true},
		{"./**", "/etc/passwd", false},
		{"src/*.go", "/home/u/proj/src/a.go", true},
		{"src/*.go", "/home/u/proj/src/sub/a.go", false},
		{"**/.env*", "/home/u/proj/.env", true},
		{"**/.env*", "/home/u/proj/deep/dir/.env.local", true},
		{"**/.env*", "/home/u/proj/env.txt", false},
		{"**/*_rsa", "/home/u/.ssh/id_rsa", true},
		{"/etc/**", "/etc/passwd", true},
		{"/etc/**", "/home/u/etc/passwd", false},
	}
	for _, c := range cases {
		if got := pathGlobMatch(c.pattern, c.path, cwd); got != c.want {
			t.Errorf("pathGlobMatch(%q, %q) = %v, want %v", c.pattern, c.path, got, c.want)
		}
	}
}

func TestCategoryOf(t *testing.T) {
	cases := map[string]Category{
		"read": CatRead, "grep": CatRead, "find": CatRead, "ls": CatRead,
		"bash_output": CatRead,
		"write":       CatWrite, "edit": CatWrite,
		"bash": CatExecute, "kill_bash": CatExecute,
		"mcp__github__get_issue": CatMCP,
		"mystery_tool":           CatExecute, // unknown tools take the strictest path
	}
	for tool, want := range cases {
		if got := CategoryOf(tool); got != want {
			t.Errorf("CategoryOf(%q) = %v, want %v", tool, got, want)
		}
	}
}

func TestRuleMatchesRespectsToolName(t *testing.T) {
	r, _ := ParseRule("read(**/.env*)", "test")
	write := CallInfo{Tool: "write", Category: CatWrite, Path: "/p/.env", Cwd: "/p"}
	if r.Matches(write) {
		t.Fatal("read rule matched a write call")
	}
	read := CallInfo{Tool: "read", Category: CatRead, Path: "/p/.env", Cwd: "/p"}
	if !r.Matches(read) {
		t.Fatal("read rule did not match a read call")
	}
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }
