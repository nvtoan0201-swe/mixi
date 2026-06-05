package config

import (
	"flag"
	"fmt"
	"io"
	"strings"
)

// Mode selects the top-level run mode after flag resolution.
type Mode string

const (
	ModeTUI    Mode = "tui"
	ModePrint  Mode = "print"
	ModeRPC    Mode = "rpc"
	ModeReplay Mode = "replay"
)

// multiFlag collects repeatable string flags (--message, --allow, --deny).
type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

// Flags holds raw parsed flag values plus which flags were explicitly set,
// so precedence resolution can tell "default" from "user said the default".
type Flags struct {
	Print              string
	Output             string
	Messages           []string
	RPC                bool
	Model              string
	Thinking           string
	Continue           bool
	Resume             string
	Fork               string
	SessionDir         string
	NoSave             bool
	ReadOnly           bool
	MaxTurns           int
	PermissionMode     string
	Allow, Deny        []string
	Cwd                string
	SystemPrompt       string
	AppendSystemPrompt string
	NoCompact          bool
	HideThinking       bool
	MaxTokens          int
	NoMCP              bool
	NoExtensions       bool
	LogLevel           string
	Verbose            bool
	PrintStats         bool
	ConfigPath         string
	Version            bool

	ReplayFile string // set by the replay subcommand
	IsReplay   bool
	Speed      string // replay pacing
	Until      string // replay cut-off entry id

	set map[string]bool
}

// WasSet reports whether the user supplied the flag explicitly.
func (f *Flags) WasSet(name string) bool { return f.set[name] }

// ParseFlags parses args (without the program name). Errors are already
// printed to errOut with usage; the caller maps them to exit code 2.
func ParseFlags(args []string, errOut io.Writer) (*Flags, error) {
	f := &Flags{set: map[string]bool{}}

	// Subcommand split: "mixi replay <file> [flags]".
	if len(args) > 0 && args[0] == "replay" {
		f.IsReplay = true
		args = args[1:]
		if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
			f.ReplayFile = args[0]
			args = args[1:]
		}
	}

	fs := flag.NewFlagSet("mixi", flag.ContinueOnError)
	fs.SetOutput(errOut)
	var messages, allow, deny multiFlag

	fs.StringVar(&f.Print, "p", "", "run one prompt headless (print mode)")
	fs.StringVar(&f.Print, "print", "", "alias of -p")
	fs.StringVar(&f.Output, "output", "text", "print-mode output: text|json")
	fs.Var(&messages, "message", "follow-up prompt (repeatable, print mode)")
	fs.BoolVar(&f.RPC, "rpc", false, "RPC mode (JSONL over stdio)")
	fs.StringVar(&f.Model, "model", "", "active model as provider/id")
	fs.StringVar(&f.Thinking, "thinking", "off", "thinking level: off|low|medium|high")
	fs.BoolVar(&f.Continue, "c", false, "continue most recent session for cwd")
	fs.BoolVar(&f.Continue, "continue", false, "alias of -c")
	fs.StringVar(&f.Resume, "resume", "", "resume a session by path or id")
	fs.StringVar(&f.Fork, "fork", "", "fork the resumed session at entry id")
	fs.StringVar(&f.SessionDir, "session-dir", "", "session storage root (default ~/.mixi/sessions)")
	fs.BoolVar(&f.NoSave, "no-save", false, "in-memory session, nothing written to disk")
	fs.BoolVar(&f.ReadOnly, "read-only", false, "open session without lock (inspect)")
	fs.IntVar(&f.MaxTurns, "max-turns", 80, "turn cap; 0 = unlimited")
	fs.StringVar(&f.PermissionMode, "permission-mode", "", "plan|prompt|auto-edit|yolo")
	fs.Var(&allow, "allow", "ad-hoc permission allow rule (repeatable)")
	fs.Var(&deny, "deny", "ad-hoc permission deny rule (repeatable)")
	fs.StringVar(&f.Cwd, "cwd", "", "working directory (default .)")
	fs.StringVar(&f.SystemPrompt, "system-prompt", "", "replace system prompt (path or text)")
	fs.StringVar(&f.AppendSystemPrompt, "append-system-prompt", "", "append to system prompt")
	fs.BoolVar(&f.NoCompact, "no-compact", false, "disable auto-compaction")
	fs.BoolVar(&f.HideThinking, "hide-thinking", false, "never render thinking")
	fs.IntVar(&f.MaxTokens, "max-tokens", 0, "per-request output cap (0 = model default)")
	fs.BoolVar(&f.NoMCP, "no-mcp", false, "skip MCP servers")
	fs.BoolVar(&f.NoExtensions, "no-extensions", false, "skip extensions")
	fs.StringVar(&f.LogLevel, "log-level", "warn", "debug|info|warn|error")
	fs.BoolVar(&f.Verbose, "verbose", false, "mirror all log records to stderr (headless modes)")
	fs.BoolVar(&f.PrintStats, "print-stats", false, "usage summary to stderr (print mode)")
	fs.StringVar(&f.Speed, "speed", "1x", "replay pacing: 1x|5x|instant")
	fs.StringVar(&f.Until, "until", "", "replay up to an entry id")
	fs.StringVar(&f.ConfigPath, "config", "", "extra settings file (highest file precedence)")
	fs.BoolVar(&f.Version, "version", false, "print version and exit")

	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	fs.Visit(func(fl *flag.Flag) { f.set[fl.Name] = true })
	// Aliases count as their canonical name too.
	if f.set["print"] {
		f.set["p"] = true
	}
	if f.set["continue"] {
		f.set["c"] = true
	}
	f.Messages, f.Allow, f.Deny = messages, allow, deny

	if rest := fs.Args(); len(rest) > 0 {
		err := fmt.Errorf("unexpected argument %q", rest[0])
		fmt.Fprintf(errOut, "mixi: %v\n", err)
		fs.Usage()
		return nil, err
	}
	if err := validateEnums(f); err != nil {
		fmt.Fprintf(errOut, "mixi: %v\n", err)
		return nil, err
	}
	return f, nil
}

func validateEnums(f *Flags) error {
	checks := []struct {
		name, val string
		ok        []string
	}{
		{"--output", f.Output, []string{"text", "json"}},
		{"--thinking", f.Thinking, []string{"off", "low", "medium", "high"}},
		{"--log-level", f.LogLevel, []string{"debug", "info", "warn", "error"}},
		{"--speed", f.Speed, []string{"1x", "5x", "instant"}},
	}
	if f.PermissionMode != "" {
		checks = append(checks, struct {
			name, val string
			ok        []string
		}{
			"--permission-mode", f.PermissionMode, []string{"plan", "prompt", "auto-edit", "yolo"},
		})
	}
	for _, c := range checks {
		valid := false
		for _, v := range c.ok {
			if c.val == v {
				valid = true
				break
			}
		}
		if !valid {
			return fmt.Errorf("%s: invalid value %q (valid: %s)", c.name, c.val, strings.Join(c.ok, "|"))
		}
	}
	if f.MaxTurns < 0 {
		return fmt.Errorf("--max-turns: must be >= 0")
	}
	if f.Fork != "" && f.Resume == "" {
		return fmt.Errorf("--fork requires --resume")
	}
	return nil
}
