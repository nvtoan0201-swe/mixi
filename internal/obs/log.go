// Package obs implements observability: structured JSON logging with daily
// file rotation, per-turn/per-session usage and cost tracking, and replay of
// recorded sessions with zero API calls.
package obs

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// keepLogFiles is how many daily log files survive pruning.
const keepLogFiles = 7

// LogOptions configures Setup.
type LogOptions struct {
	// Dir is the log directory; "" resolves to ~/.mixi/logs.
	Dir string
	// Level filters the file handler (and the mirror when Verbose).
	Level slog.Level
	// Mirror additionally receives human-readable records — headless modes
	// pass stderr; the TUI passes nil because the renderer owns the terminal.
	Mirror io.Writer
	// Verbose lowers the mirror threshold from WARN to Level.
	Verbose bool
}

// Setup builds the process logger: JSON records appended to a daily-rotated
// file, plus an optional text mirror. Logging must never break a run, so an
// unopenable log dir degrades to mirror-only (or discard).
func Setup(o LogOptions) (logger *slog.Logger, closeLog func()) {
	var handlers []slog.Handler
	closeLog = func() {}
	if f, err := openDaily(o.Dir); err == nil {
		handlers = append(handlers, slog.NewJSONHandler(f, &slog.HandlerOptions{
			Level:       o.Level,
			ReplaceAttr: renameTimeToTS,
		}))
		closeLog = func() { f.Close() }
	}
	if o.Mirror != nil {
		// WARN+ reaches the terminal by default; --verbose (or an even
		// stricter --log-level) moves the mirror to the configured level.
		lv := slog.LevelWarn
		if o.Verbose || o.Level > lv {
			lv = o.Level
		}
		handlers = append(handlers, slog.NewTextHandler(o.Mirror, &slog.HandlerOptions{Level: lv}))
	}
	switch len(handlers) {
	case 0:
		return slog.New(slog.DiscardHandler), closeLog
	case 1:
		return slog.New(handlers[0]), closeLog
	default:
		return slog.New(multiHandler(handlers)), closeLog
	}
}

// renameTimeToTS maps slog's built-in time key onto the log field spec's
// "ts" name; everything else passes through.
func renameTimeToTS(groups []string, a slog.Attr) slog.Attr {
	if len(groups) == 0 && a.Key == slog.TimeKey {
		a.Key = "ts"
	}
	return a
}

// openDaily opens (creating if needed) today's log file and prunes old
// ones. The name is fixed at startup: a session running past midnight keeps
// its file — rotation is a per-process concern, not a wall-clock one.
func openDaily(dir string) (*os.File, error) {
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		dir = filepath.Join(home, ".mixi", "logs")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	name := "mixi-" + time.Now().UTC().Format("2006-01-02") + ".jsonl"
	f, err := os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	pruneOldLogs(dir)
	return f, nil
}

// pruneOldLogs removes all but the newest keepLogFiles daily logs. The
// date-stamped names make lexicographic order age order. Removal failures
// are ignored — pruning is best-effort housekeeping.
func pruneOldLogs(dir string) {
	matches, _ := filepath.Glob(filepath.Join(dir, "mixi-*.jsonl"))
	if len(matches) <= keepLogFiles {
		return
	}
	sort.Strings(matches)
	for _, p := range matches[:len(matches)-keepLogFiles] {
		os.Remove(p)
	}
}

// LevelFromConfig resolves the effective log level: an explicit --log-level
// beats MIXI_LOG, which beats the flag default.
func LevelFromConfig(flagVal string, flagWasSet bool) slog.Level {
	if !flagWasSet {
		if lv, ok := ParseLevel(os.Getenv("MIXI_LOG")); ok {
			return lv
		}
	}
	if lv, ok := ParseLevel(flagVal); ok {
		return lv
	}
	return slog.LevelWarn
}

// ParseLevel maps the CLI level names onto slog levels.
func ParseLevel(s string) (slog.Level, bool) {
	switch s {
	case "debug":
		return slog.LevelDebug, true
	case "info":
		return slog.LevelInfo, true
	case "warn":
		return slog.LevelWarn, true
	case "error":
		return slog.LevelError, true
	}
	return 0, false
}

// multiHandler fans one record out to several handlers (file + mirror).
type multiHandler []slog.Handler

func (h multiHandler) Enabled(ctx context.Context, lv slog.Level) bool {
	for _, hh := range h {
		if hh.Enabled(ctx, lv) {
			return true
		}
	}
	return false
}

func (h multiHandler) Handle(ctx context.Context, r slog.Record) error {
	var firstErr error
	for _, hh := range h {
		if !hh.Enabled(ctx, r.Level) {
			continue
		}
		if err := hh.Handle(ctx, r.Clone()); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (h multiHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	out := make(multiHandler, len(h))
	for i, hh := range h {
		out[i] = hh.WithAttrs(attrs)
	}
	return out
}

func (h multiHandler) WithGroup(name string) slog.Handler {
	out := make(multiHandler, len(h))
	for i, hh := range h {
		out[i] = hh.WithGroup(name)
	}
	return out
}
