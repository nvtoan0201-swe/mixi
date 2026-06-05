package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/user/mixi-agent/internal/ai"
	"github.com/user/mixi-agent/internal/ai/faux"
)

// DefaultModel is used when neither flags nor settings pick one.
const DefaultModel = "anthropic/claude-sonnet-4-6"

// RuntimeConfig is the fully resolved configuration main dispatches on.
type RuntimeConfig struct {
	Mode    Mode
	Flags   *Flags // raw flags for mode-specific details
	Setting Settings

	Model          ai.Model
	Thinking       ai.ThinkingLevel
	Prompt         string // print-mode prompt (flag and/or stdin)
	Messages       []string
	OutputJSON     bool
	PrintStats     bool
	Cwd            string
	SessionDir     string
	MaxTurns       int
	MaxTokens      int
	PermissionMode string
	Fsync          bool
	LogLevel       string
	Verbose        bool
}

// Resolve applies precedence (flags > settings > defaults) and decides the
// run mode. stdin is the piped standard input ("" when stdin is a TTY);
// a pipe makes print mode implicit, mirroring `mixi -p`.
func Resolve(f *Flags, s Settings, cwd, stdin string) (*RuntimeConfig, error) {
	rc := &RuntimeConfig{Flags: f, Setting: s, Cwd: cwd}

	switch {
	case f.IsReplay:
		rc.Mode = ModeReplay
	case f.RPC:
		rc.Mode = ModeRPC
	case f.WasSet("p") || stdin != "":
		rc.Mode = ModePrint
	default:
		rc.Mode = ModeTUI
	}

	// Prompt: flag text and piped stdin combine, prompt text first — the
	// pipe is usually data the prompt refers to.
	parts := []string{}
	if f.Print != "" {
		parts = append(parts, f.Print)
	}
	if stdin != "" {
		parts = append(parts, stdin)
	}
	rc.Prompt = strings.Join(parts, "\n\n")
	rc.Messages = f.Messages
	rc.OutputJSON = f.Output == "json"
	rc.PrintStats = f.PrintStats

	spec := DefaultModel
	if s.Model.Default != "" {
		spec = s.Model.Default
	}
	if f.WasSet("model") {
		spec = f.Model
	}
	model, err := ResolveModel(spec)
	if err != nil {
		return nil, err
	}
	rc.Model = model

	rc.Thinking = ai.ThinkingLevel(f.Thinking)
	rc.MaxTurns = f.MaxTurns
	rc.MaxTokens = f.MaxTokens
	rc.Fsync = s.Files.Fsync
	rc.LogLevel = f.LogLevel
	rc.Verbose = f.Verbose

	rc.PermissionMode = "prompt"
	if s.Permissions.Mode != "" {
		rc.PermissionMode = s.Permissions.Mode
	}
	if f.PermissionMode != "" {
		rc.PermissionMode = f.PermissionMode
	}

	rc.SessionDir = f.SessionDir
	if rc.SessionDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("config: resolve home dir: %w", err)
		}
		rc.SessionDir = home + string(os.PathSeparator) + ".mixi" + string(os.PathSeparator) + "sessions"
	}
	return rc, nil
}

// ResolveModel turns a "provider/id" spec into a catalog model. The faux
// provider lives outside the catalog: it exists only when named explicitly.
func ResolveModel(spec string) (ai.Model, error) {
	provider, id, ok := strings.Cut(spec, "/")
	if !ok || provider == "" || id == "" {
		return ai.Model{}, fmt.Errorf("config: --model wants provider/id, got %q", spec)
	}
	if provider == "faux" && id == "scripted" {
		return faux.Model(), nil
	}
	m, found := ai.Lookup(provider, id)
	if !found {
		return ai.Model{}, fmt.Errorf("config: unknown model %q (known: %s)", spec, knownModels())
	}
	return m, nil
}

func knownModels() string {
	var specs []string
	for _, m := range ai.Models() {
		specs = append(specs, m.Provider+"/"+m.ID)
	}
	specs = append(specs, "faux/scripted")
	return strings.Join(specs, ", ")
}
