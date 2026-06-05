// Command mixi is the coding-agent CLI. This phase ships print mode
// (`mixi -p "prompt"`); TUI, RPC, and replay modes land in later phases and
// currently exit with a "not yet available" notice.
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

	switch rc.Mode {
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

	// TUI mode owns the terminal: logs must never hit stdout/stderr, only a
	// file (full observability wiring is a later concern — the guard is not).
	logW, closeLog := logWriter(rc, stderr)
	defer closeLog()
	log := newLogger(logW, rc.LogLevel)
	store, err := openSession(rc, cwd, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "mixi: %v\n", err)
		return modes.ExitUsage
	}
	defer store.Close()

	ws := workset.New()
	reg := tools.NewRegistry()
	jobs, err := tools.RegisterBuiltins(reg, tools.Options{Cwd: cwd, Fsync: rc.Fsync, Observer: ws})
	if err != nil {
		fmt.Fprintf(stderr, "mixi: register tools: %v\n", err)
		return modes.ExitRunErr
	}
	defer jobs.KillAll()

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
		Log:        log,
	})
	hooks := ctrl.Hooks()
	hooks.GetAPIKey = apiKeyFromEnv

	// The TUI answers permission asks through a modal; the asker publishes on
	// the agent bus once the agent exists (notifier breaks the cycle).
	var notifier agentNotifier
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
		Filters:      []agent.ToolCallFilter{permissionFilter{eng}},
		SystemPrompt: systemPrompt(f, reg, cwd),
		StreamOpts:   streamOpts(rc, store),
		MaxTurns:     maxTurns(rc.MaxTurns),
		History:      ctrl.LoadHistory(),
		Hooks:        hooks,
		Log:          log,
	})
	ctrl.SetNotify(a.Notify)
	notifier.set(a)

	stopSignals := handleSignals(a, jobs, store)
	defer stopSignals()

	if rc.Mode == config.ModeTUI {
		return runTUI(a, eng, ctrl, store, jobs, rc, stderr)
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

// logWriter picks the slog destination: stderr for headless modes, a file
// (or discard) for the TUI so the renderer owns the terminal exclusively.
func logWriter(rc *config.RuntimeConfig, stderr io.Writer) (io.Writer, func()) {
	if rc.Mode != config.ModeTUI {
		return stderr, func() {}
	}
	if err := os.MkdirAll(rc.SessionDir, 0o700); err != nil {
		return io.Discard, func() {}
	}
	f, err := os.OpenFile(filepath.Join(rc.SessionDir, "mixi.log"),
		os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return io.Discard, func() {}
	}
	return f, func() { f.Close() }
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

func newLogger(w io.Writer, level string) *slog.Logger {
	var lv slog.Level
	switch level {
	case "debug":
		lv = slog.LevelDebug
	case "info":
		lv = slog.LevelInfo
	case "error":
		lv = slog.LevelError
	default:
		lv = slog.LevelWarn
	}
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: lv}))
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
