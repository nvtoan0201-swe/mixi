package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/user/mixi-agent/internal/agent"
	"github.com/user/mixi-agent/internal/ai"
	"github.com/user/mixi-agent/internal/config"
	"github.com/user/mixi-agent/internal/session"
	"github.com/user/mixi-agent/internal/tools"
)

// openSession picks the session per flags: fresh (default), most recent
// (-c), a specific file or id (--resume, optionally forked at an entry), or
// a memory-only store (--no-save).
func openSession(rc *config.RuntimeConfig, cwd string, stderr io.Writer) (session.Storage, error) {
	f := rc.Flags
	m := &session.Manager{Root: rc.SessionDir, Opts: session.Options{
		ReadOnly: f.ReadOnly,
		Fsync:    rc.Fsync,
	}}
	switch {
	case f.NoSave:
		return m.InMemory(cwd), nil
	case f.Resume != "":
		path, err := resolveSessionPath(m, cwd, f.Resume)
		if err != nil {
			return nil, err
		}
		if f.Fork != "" {
			return m.Fork(path, f.Fork)
		}
		return m.Open(path)
	case f.Continue:
		s, err := m.ContinueRecent(cwd)
		if errNotExist(err) {
			fmt.Fprintln(stderr, "mixi: no previous session for this directory — starting a new one")
			return m.Create(cwd)
		}
		return s, err
	default:
		return m.Create(cwd)
	}
}

// resolveSessionPath accepts a session file path or a session/file id
// fragment, matched against this cwd's session directory.
func resolveSessionPath(m *session.Manager, cwd, ref string) (string, error) {
	if fi, err := os.Stat(ref); err == nil && !fi.IsDir() {
		return ref, nil
	}
	matches, _ := filepath.Glob(filepath.Join(m.Dir(cwd), "*"+ref+"*.jsonl"))
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return "", fmt.Errorf("--resume: no session matches %q", ref)
	default:
		return "", fmt.Errorf("--resume: %q is ambiguous (%d matches)", ref, len(matches))
	}
}

// systemPrompt resolves --system-prompt (a file path or literal text, with
// --append-system-prompt added) or builds the default prompt.
func systemPrompt(f *config.Flags, reg *tools.Registry, cwd string) string {
	prompt := ""
	if f.SystemPrompt != "" {
		prompt = f.SystemPrompt
		if raw, err := os.ReadFile(f.SystemPrompt); err == nil {
			prompt = string(raw)
		}
	} else {
		prompt = agent.BuildSystemPrompt(agent.SysPromptOptions{Tools: reg.All(), CWD: cwd})
	}
	if f.AppendSystemPrompt != "" {
		prompt = strings.TrimRight(prompt, "\n") + "\n\n" + f.AppendSystemPrompt
	}
	return prompt
}

// streamOpts maps resolved config onto per-request stream options. The
// session id keys provider-side prompt caching.
func streamOpts(rc *config.RuntimeConfig, store session.Storage) ai.StreamOptions {
	return ai.StreamOptions{
		Thinking:  rc.Thinking,
		MaxTokens: rc.MaxTokens,
		SessionID: store.Header().ID,
	}
}
