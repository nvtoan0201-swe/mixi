// Command mixi is the coding-agent CLI: interactive TUI by default, print
// mode (`mixi -p "prompt"`), and `mixi replay <session.jsonl>`; RPC mode
// lands in a later phase and currently exits with a "not yet available"
// notice.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/user/mixi-agent/internal/agent"
	_ "github.com/user/mixi-agent/internal/ai/anthropic" // registers provider
	"github.com/user/mixi-agent/internal/compact"
	"github.com/user/mixi-agent/internal/config"
	"github.com/user/mixi-agent/internal/modes"
	"github.com/user/mixi-agent/internal/obs"
	"github.com/user/mixi-agent/internal/perm"
	"github.com/user/mixi-agent/internal/session"
	"github.com/user/mixi-agent/internal/tools"
	"github.com/user/mixi-agent/internal/workset"
)

// version is stamped by the release build via -ldflags.
var version = "dev"

func main() {
	piped, _ := stdinPiped()
	os.Exit(realMain(os.Args[1:], os.Stdin, piped, os.Stdout, os.Stderr))
}

// stdinPiped reports whether stdin carries piped data rather than a TTY.
func stdinPiped() (bool, error) {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false, err
	}
	return fi.Mode()&os.ModeCharDevice == 0, nil
}

// realMain is the testable entry point; it returns the process exit code.
// stdinR is consumed only when piped and a prompt is actually wanted, so
// `mixi --version < pipe` never blocks on a silent writer.
func realMain(args []string, stdinR io.Reader, piped bool, stdout, stderr io.Writer) int {
	f, err := config.ParseFlags(args, stderr)
	if err != nil {
		return modes.ExitUsage
	}
	if f.Version {
		fmt.Fprintf(stdout, "mixi %s\n", version)
		return modes.ExitOK
	}
	stdin := ""
	if piped {
		raw, err := io.ReadAll(stdinR)
		if err != nil {
			fmt.Fprintf(stderr, "mixi: read stdin: %v\n", err)
			return modes.ExitUsage
		}
		stdin = string(raw)
	}

	cwd := f.Cwd
	if cwd == "" {
		cwd, err = os.Getwd()
		if err != nil {
			fmt.Fprintf(stderr, "mixi: resolve cwd: %v\n", err)
			return modes.ExitUsage
		}
	}
	cwd, err = filepath.Abs(cwd)
	if err != nil {
		fmt.Fprintf(stderr, "mixi: %v\n", err)
		return modes.ExitUsage
	}

	settings, err := config.LoadSettings(cwd, f.ConfigPath)
	if err != nil {
		fmt.Fprintf(stderr, "mixi: %v\n", err)
		return modes.ExitUsage
	}
	rc, err := config.Resolve(f, settings, cwd, stdin)
	if err != nil {
		fmt.Fprintf(stderr, "mixi: %v\n", err)
		return modes.ExitUsage
	}

	// Observability comes up first so every later subsystem logs through it.
	// The TUI owns the terminal exclusively, so it gets no stderr mirror —
	// records go only to the daily log file.
	var mirror io.Writer
	if rc.Mode != config.ModeTUI {
		mirror = stderr
	}
	log, closeLog := obs.Setup(obs.LogOptions{
		Level:   obs.LevelFromConfig(rc.LogLevel, f.WasSet("log-level")),
		Mirror:  mirror,
		Verbose: rc.Verbose,
	})
	defer closeLog()
	// Package-level slog call sites (session crash recovery) follow the same
	// policy instead of leaking onto the terminal.
	slog.SetDefault(log)

	switch rc.Mode {
	case config.ModeReplay:
		return runReplay(rc, stdout, stderr)
	case config.ModePrint, config.ModeTUI:
	default:
		fmt.Fprintf(stderr, "mixi: %s mode is not yet available\n", rc.Mode)
		return modes.ExitUsage
	}
	if rc.Mode == config.ModePrint && rc.Prompt == "" {
		fmt.Fprintln(stderr, "mixi: print mode needs a prompt (-p \"...\" or piped stdin)")
		return modes.ExitUsage
	}
	if hint := missingAPIKey(rc.Model.Provider); hint != "" {
		fmt.Fprintf(stderr, "mixi: %s\n", hint)
		return modes.ExitUsage
	}

	store, err := openSession(rc, cwd, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "mixi: %v\n", err)
		return modes.ExitUsage
	}
	defer store.Close()
	log = log.With("session_id", store.Header().ID)

	ws := workset.New()
	reg := tools.NewRegistry()
	jobs, err := tools.RegisterBuiltins(reg, tools.Options{Cwd: cwd, Fsync: rc.Fsync, Observer: ws})
	if err != nil {
		fmt.Fprintf(stderr, "mixi: register tools: %v\n", err)
		return modes.ExitRunErr
	}
	defer jobs.KillAll()

	// The notifier forwards permission asks and subsystem notices onto the
	// agent bus once the agent exists (it breaks construction cycles).
	var notifier agentNotifier

	mcpMgr, err := startMCP(rc, reg, log.With("component", "mcp"), notifier.notice)
	if err != nil {
		fmt.Fprintf(stderr, "mixi: %v\n", err)
		return modes.ExitUsage
	}
	if mcpMgr != nil {
		defer mcpMgr.Close()
	}

	extHost, err := startExtensions(rc, cwd, store, reg, log.With("component", "ext"), notifier.notice)
	if err != nil {
		fmt.Fprintf(stderr, "mixi: %v\n", err)
		return modes.ExitUsage
	}
	if extHost != nil {
		defer extHost.Close()
	}

	apiKey, _ := apiKeyFromEnv(rc.Model.Provider)
	ctrl := compact.NewController(compact.ControllerConfig{
		Store:      store,
		WS:         ws,
		Summarizer: &compact.LLMSummarizer{Model: rc.Model, APIKey: apiKey},
		Model:      rc.Model,
		MaxTokens:  rc.MaxTokens,
		Reserve:    settings.Compaction.ReserveTokens,
		KeepRecent: settings.Compaction.KeepRecentTokens,
		Enabled:    !settings.Compaction.Disabled,
		Log:        log.With("component", "compact"),
	})
	hooks := ctrl.Hooks()
	hooks.GetAPIKey = apiKeyFromEnv

	// The TUI answers permission asks through a modal, routed via the
	// notifier above.
	var asker perm.Asker
	if rc.Mode == config.ModeTUI {
		asker = perm.NotifyAsker{Notify: notifier.publish}
	}
	eng, err := buildPermissionEngine(rc, cwd, asker)
	if err != nil {
		fmt.Fprintf(stderr, "mixi: %v\n", err)
		return modes.ExitUsage
	}

	a := agent.New(agent.Config{
		Model:        rc.Model,
		Tools:        reg,
		Filters:      append([]agent.ToolCallFilter{permissionFilter{eng}}, extensionFilters(extHost)...),
		SystemPrompt: systemPrompt(f, reg, cwd),
		StreamOpts:   streamOpts(rc, store),
		MaxTurns:     maxTurns(rc.MaxTurns),
		History:      ctrl.LoadHistory(),
		Hooks:        hooks,
		Log:          log.With("component", "agent"),
	})
	ctrl.SetNotify(a.Notify)
	notifier.set(a)
	if extHost != nil {
		unbind := bindExtensions(extHost, a, store, &notifier, rc.Mode != config.ModeTUI, stderr, log)
		defer unbind()
	}

	stopSignals := handleSignals(a, jobs, store)
	defer stopSignals()

	if rc.Mode == config.ModeTUI {
		return runTUI(a, eng, ctrl, store, jobs, mcpMgr, extHost, rc, stderr)
	}
	return modes.RunPrint(context.Background(), modes.PrintDeps{
		Agent: a, Out: stdout, ErrOut: stderr, Log: log,
	}, modes.PrintOptions{
		Prompt:     rc.Prompt,
		Messages:   rc.Messages,
		JSON:       rc.OutputJSON,
		PrintStats: rc.PrintStats,
	})
}

// handleSignals maps SIGINT to a graceful abort (the run ends with exit
// 130), a second SIGINT or SIGTERM to immediate cleanup and exit.
func handleSignals(a *agent.Agent, jobs *tools.JobTable, store session.Storage) (stop func()) {
	ch := make(chan os.Signal, 2)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	go func() {
		interrupted := false
		for sig := range ch {
			if sig == os.Interrupt && !interrupted {
				interrupted = true
				a.Abort()
				continue
			}
			// Second SIGINT or SIGTERM: kill children, release the session
			// lock, leave. The session is already durable per appended event.
			jobs.KillAll()
			store.Close()
			if sig == os.Interrupt {
				os.Exit(modes.ExitSIGINT)
			}
			os.Exit(143) // 128 + SIGTERM
		}
	}()
	return func() { signal.Stop(ch); close(ch) }
}

func maxTurns(n int) int {
	if n == 0 {
		return -1 // flag contract: 0 = unlimited; agent contract: <0 = unlimited
	}
	return n
}

var apiKeyEnvByProvider = map[string]string{
	"anthropic": "ANTHROPIC_API_KEY",
	"openai":    "OPENAI_API_KEY",
}

func apiKeyFromEnv(provider string) (string, error) {
	if env, ok := apiKeyEnvByProvider[provider]; ok {
		return os.Getenv(env), nil
	}
	return "", nil
}

// missingAPIKey returns a usage hint when the chosen provider needs a key
// that is not in the environment — better than failing on the first call.
func missingAPIKey(provider string) string {
	env, ok := apiKeyEnvByProvider[provider]
	if !ok || os.Getenv(env) != "" {
		return ""
	}
	return fmt.Sprintf("provider %q needs the %s environment variable", provider, env)
}

// errNotExist unifies the two shapes the session package reports a missing
// session with (raw fs error or wrapped).
func errNotExist(err error) bool { return errors.Is(err, os.ErrNotExist) }
