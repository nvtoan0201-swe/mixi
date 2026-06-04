package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

type findTool struct{ cwd string }

func (t *findTool) Name() string { return "find" }

func (t *findTool) Description() string {
	return "Find files by glob pattern (e.g. *.go, src/**/*.ts). Returns matching paths."
}

func (t *findTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{
"pattern":{"type":"string","description":"Glob pattern; include / to match against full paths"},
"path":{"type":"string","description":"Directory to search; defaults to cwd"},
"limit":{"type":"integer","minimum":1,"default":1000,"description":"Max results"}
},"required":["pattern"]}`)
}

func (t *findTool) Mode() ExecMode { return ExecParallel }

func (t *findTool) Execute(ctx context.Context, args json.RawMessage, _ chan<- ToolUpdate) (ToolResult, error) {
	var a struct {
		Pattern string `json:"pattern"`
		Path    string `json:"path"`
		Limit   int    `json:"limit"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return ToolResult{}, fmt.Errorf("find: bad arguments: %w", err)
	}
	if a.Limit <= 0 {
		a.Limit = 1000
	}
	if fd, ok := findBinary("fd", "fdfind"); ok {
		return t.runFd(ctx, fd, a.Pattern, a.Path, a.Limit)
	}
	return t.walkFallback(ctx, a.Pattern, a.Path, a.Limit)
}

// runFd shells out to fd, which is fast and honors .gitignore. It asks for
// limit+1 results so a count of exactly limit isn't misreported as truncated.
func (t *findTool) runFd(ctx context.Context, fd, pattern, path string, limit int) (ToolResult, error) {
	argv := []string{"--glob", "--color=never", "--hidden", "--no-require-git",
		"--max-results", strconv.Itoa(limit + 1)}
	if strings.Contains(pattern, "/") {
		argv = append(argv, "--full-path")
		if !strings.HasPrefix(pattern, "**/") && !strings.HasPrefix(pattern, "/") {
			pattern = "**/" + pattern
		}
	}
	argv = append(argv, "--", pattern)
	if path != "" {
		argv = append(argv, path)
	}
	cmd := exec.CommandContext(ctx, fd, argv...)
	cmd.Dir = t.cwd
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return commandFailedError("find (fd)", err, stderr.String()), nil
	}
	lines := strings.Split(strings.TrimRight(stdout.String(), "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return Text("no files matched"), nil
	}
	truncated := len(lines) > limit
	if truncated {
		lines = lines[:limit]
	}
	out := strings.Join(lines, "\n")
	if truncated {
		out += fmt.Sprintf("\n[truncated at %d results]", limit)
	}
	return Text(out), nil
}

// walkFallback is the pure-Go path when fd is absent: WalkDir + doublestar.
// It skips .git but knows nothing about .gitignore — the output says so.
func (t *findTool) walkFallback(ctx context.Context, pattern, path string, limit int) (ToolResult, error) {
	root := t.cwd
	if path != "" {
		root = resolvePath(t.cwd, path)
	}
	fullPath := strings.Contains(pattern, "/")
	if fullPath && !strings.HasPrefix(pattern, "**/") {
		pattern = "**/" + pattern
	}

	var matches []string
	truncated := false
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable entries are skipped, not fatal
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		if d.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		target := filepath.Base(p)
		if fullPath {
			target = rel
		}
		if ok, _ := doublestar.Match(pattern, target); !ok {
			return nil
		}
		if path != "" {
			matches = append(matches, filepath.Join(path, filepath.FromSlash(rel)))
		} else {
			matches = append(matches, filepath.FromSlash(rel))
		}
		// Walk one past the limit so an exactly-limit result set isn't
		// misreported as truncated.
		if len(matches) > limit {
			matches = matches[:limit]
			truncated = true
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil && err != filepath.SkipAll {
		return ToolResult{}, fmt.Errorf("find: %w", err)
	}

	if len(matches) == 0 {
		return Text("no files matched\n[fd not found: .gitignore not respected]"), nil
	}
	out := strings.Join(matches, "\n")
	if truncated {
		out += fmt.Sprintf("\n[truncated at %d results]", limit)
	}
	out += "\n[fd not found: .gitignore not respected]"
	return Text(out), nil
}
