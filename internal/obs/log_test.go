package obs

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// readLogLines parses every JSON record from the day's log file.
func readLogLines(t *testing.T, dir string) []map[string]any {
	t.Helper()
	name := "mixi-" + time.Now().UTC().Format("2006-01-02") + ".jsonl"
	f, err := os.Open(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var recs []map[string]any
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatalf("malformed log line %q: %v", sc.Text(), err)
		}
		recs = append(recs, m)
	}
	return recs
}

// TestLogSchema: records carry the standard field set — ts, level, msg plus
// the wired component/session_id and event-specific fields like turn.
func TestLogSchema(t *testing.T) {
	dir := t.TempDir()
	log, closeLog := Setup(LogOptions{Dir: dir, Level: slog.LevelDebug})
	log = log.With("session_id", "s-123")
	log.With("component", "agent").Info("turn start", "turn", 3)
	log.With("component", "tools.bash").Debug("exec", "tool", "bash", "call_id", "c1", "latency_ms", 12)
	closeLog()

	recs := readLogLines(t, dir)
	if len(recs) != 2 {
		t.Fatalf("got %d records, want 2", len(recs))
	}
	for i, rec := range recs {
		for _, key := range []string{"ts", "level", "msg", "component", "session_id"} {
			if _, ok := rec[key]; !ok {
				t.Errorf("record %d missing %q: %v", i, key, rec)
			}
		}
		if _, ok := rec["time"]; ok {
			t.Errorf("record %d still has slog's default time key", i)
		}
	}
	if recs[0]["turn"] != float64(3) || recs[0]["level"] != "INFO" {
		t.Errorf("record 0 = %v", recs[0])
	}
	if recs[1]["tool"] != "bash" || recs[1]["call_id"] != "c1" {
		t.Errorf("record 1 = %v", recs[1])
	}
}

// TestMirrorLevels: the stderr mirror shows WARN+ by default and everything
// at the configured level with Verbose; the file always gets the full level.
func TestMirrorLevels(t *testing.T) {
	dir := t.TempDir()
	var mirror bytes.Buffer
	log, closeLog := Setup(LogOptions{Dir: dir, Level: slog.LevelDebug, Mirror: &mirror})
	log.Debug("quiet detail")
	log.Warn("loud problem")
	closeLog()
	if got := mirror.String(); bytes.Contains([]byte(got), []byte("quiet detail")) || !bytes.Contains([]byte(got), []byte("loud problem")) {
		t.Errorf("default mirror = %q, want WARN only", got)
	}
	if recs := readLogLines(t, dir); len(recs) != 2 {
		t.Errorf("file got %d records, want both", len(recs))
	}

	mirror.Reset()
	log, closeLog = Setup(LogOptions{Dir: t.TempDir(), Level: slog.LevelDebug, Mirror: &mirror, Verbose: true})
	log.Debug("quiet detail")
	closeLog()
	if !bytes.Contains(mirror.Bytes(), []byte("quiet detail")) {
		t.Errorf("verbose mirror = %q, want debug record", mirror.String())
	}
}

// TestNoMirrorFileOnly: TUI mode passes no mirror — even ERROR records go
// only to the file (the renderer owns the terminal).
func TestNoMirrorFileOnly(t *testing.T) {
	dir := t.TempDir()
	log, closeLog := Setup(LogOptions{Dir: dir, Level: slog.LevelDebug})
	log.Error("must stay off the terminal")
	closeLog()
	recs := readLogLines(t, dir)
	if len(recs) != 1 || recs[0]["msg"] != "must stay off the terminal" {
		t.Fatalf("file records = %v, want the error record", recs)
	}
}

// TestPruneKeepsNewestSeven: startup removes the oldest daily files beyond
// the keep window, never the newest ones.
func TestPruneKeepsNewestSeven(t *testing.T) {
	dir := t.TempDir()
	for i := 1; i <= 9; i++ {
		name := filepath.Join(dir, fmt.Sprintf("mixi-2026-01-%02d.jsonl", i))
		if err := os.WriteFile(name, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	_, closeLog := Setup(LogOptions{Dir: dir})
	closeLog()
	matches, _ := filepath.Glob(filepath.Join(dir, "mixi-*.jsonl"))
	if len(matches) != keepLogFiles {
		t.Fatalf("got %d files after prune, want %d: %v", len(matches), keepLogFiles, matches)
	}
	for _, gone := range []string{"mixi-2026-01-01.jsonl", "mixi-2026-01-02.jsonl", "mixi-2026-01-03.jsonl"} {
		if _, err := os.Stat(filepath.Join(dir, gone)); err == nil {
			t.Errorf("%s survived pruning", gone)
		}
	}
}

// TestLevelFromConfig: explicit flag > MIXI_LOG > flag default.
func TestLevelFromConfig(t *testing.T) {
	t.Setenv("MIXI_LOG", "debug")
	if lv := LevelFromConfig("warn", false); lv != slog.LevelDebug {
		t.Errorf("env override: got %v, want debug", lv)
	}
	if lv := LevelFromConfig("error", true); lv != slog.LevelError {
		t.Errorf("explicit flag: got %v, want error", lv)
	}
	t.Setenv("MIXI_LOG", "bogus")
	if lv := LevelFromConfig("info", false); lv != slog.LevelInfo {
		t.Errorf("bad env falls back to flag: got %v, want info", lv)
	}
}

// TestSetupUnwritableDirDegrades: a hostile log dir must not break the run;
// the logger degrades to the mirror.
func TestSetupUnwritableDirDegrades(t *testing.T) {
	var mirror bytes.Buffer
	log, closeLog := Setup(LogOptions{Dir: filepath.Join(os.DevNull, "nope"), Mirror: &mirror})
	defer closeLog()
	log.Warn("still alive")
	if !bytes.Contains(mirror.Bytes(), []byte("still alive")) {
		t.Errorf("mirror = %q, want the record", mirror.String())
	}
}
