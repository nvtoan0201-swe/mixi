package perm

import (
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/user/mixi-agent/internal/ai"
)

// CallInfo is the rule-matching view of one tool call: its category plus the
// command (execute) or cleaned absolute path (file tools) the rules inspect.
type CallInfo struct {
	Tool     string
	Category Category
	Command  string // bash only
	Path     string // read/write/edit only, cleaned absolute
	Cwd      string
}

// describeCall projects a tool call into CallInfo. Unparseable args yield a
// zero Command/Path — rules then can't match, so the call falls through to
// mode defaults (schema validation rejects it properly after the gate).
func describeCall(call ai.ToolCall, cwd string) CallInfo {
	info := CallInfo{Tool: call.Name, Category: CategoryOf(call.Name), Cwd: cwd}
	switch call.Name {
	case "bash":
		var a struct {
			Command string `json:"command"`
		}
		if json.Unmarshal(call.Args, &a) == nil {
			info.Command = a.Command
		}
	case "read", "write", "edit", "grep", "find", "ls":
		var a struct {
			Path string `json:"path"`
		}
		if json.Unmarshal(call.Args, &a) == nil && a.Path != "" {
			if filepath.IsAbs(a.Path) {
				info.Path = filepath.Clean(a.Path)
			} else {
				info.Path = filepath.Join(cwd, a.Path)
			}
		}
	}
	return info
}

// secretGlobs are read patterns that always require explicit approval —
// credential material must never flow to a model silently.
var secretGlobs = []string{"**/.env*", "**/*_rsa", "**/credentials*"}

// matchesSecretGlob reports whether the absolute path looks like credential
// material.
func matchesSecretGlob(absPath, cwd string) bool {
	for _, g := range secretGlobs {
		if pathGlobMatch(g, absPath, cwd) {
			return true
		}
	}
	return false
}

// outsideCwd reports whether absPath escapes the cwd subtree after symlink
// resolution, so a link inside the tree can't smuggle writes outside it.
func outsideCwd(absPath, cwd string) bool {
	realCwd, err := filepath.EvalSymlinks(cwd)
	if err != nil {
		realCwd = cwd
	}
	real := resolveDeepestExisting(absPath)
	rel, err := filepath.Rel(realCwd, real)
	if err != nil {
		return true
	}
	return rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// resolveDeepestExisting resolves symlinks for the longest existing prefix of
// path and rejoins the not-yet-created remainder, so new files in symlinked
// directories still resolve to their real location.
func resolveDeepestExisting(path string) string {
	dir, rest := path, ""
	for {
		if resolved, err := filepath.EvalSymlinks(dir); err == nil {
			return filepath.Join(resolved, rest)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return path
		}
		rest = filepath.Join(filepath.Base(dir), rest)
		dir = parent
	}
}
