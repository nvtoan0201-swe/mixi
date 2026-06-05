package obs

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/user/mixi-agent/internal/session"
)

const replayFixture = "testdata/example_session.jsonl"

// TestReplayGolden: instant replay of the example session reproduces the
// golden transcript byte-exact. Regenerate with MIXI_UPDATE_GOLDEN=1 and
// review the diff by hand.
func TestReplayGolden(t *testing.T) {
	var out bytes.Buffer
	if err := RunReplay(replayFixture, ReplayOptions{Speed: "instant", Out: &out}); err != nil {
		t.Fatal(err)
	}
	goldenPath := "testdata/example_replay_transcript.golden"
	if os.Getenv("MIXI_UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(goldenPath, out.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out.Bytes(), want) {
		t.Errorf("transcript mismatch\n--- got ---\n%s\n--- want ---\n%s", out.Bytes(), want)
	}
}

// TestReplayUntilStopsEarly: --until cuts the replay after the named entry.
func TestReplayUntilStopsEarly(t *testing.T) {
	var out bytes.Buffer
	err := RunReplay(replayFixture, ReplayOptions{Speed: "instant", Until: "c3d4e5f6", Out: &out})
	if err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "func main()") {
		t.Errorf("missing content up to the cut:\n%s", got)
	}
	if strings.Contains(got, "Added the --version flag.") {
		t.Errorf("content after --until leaked:\n%s", got)
	}
}

// TestReplayUntilOffPath: an --until id not on the active path errors
// instead of silently replaying everything.
func TestReplayUntilOffPath(t *testing.T) {
	err := RunReplay(replayFixture, ReplayOptions{Speed: "instant", Until: "ffffffff", Out: &bytes.Buffer{}})
	if err == nil || !strings.Contains(err.Error(), "not on the active path") {
		t.Fatalf("err = %v, want off-path error", err)
	}
}

// TestReplayBadSpeed rejects unknown speed values.
func TestReplayBadSpeed(t *testing.T) {
	err := RunReplay(replayFixture, ReplayOptions{Speed: "2x", Out: &bytes.Buffer{}})
	if err == nil || !strings.Contains(err.Error(), "invalid speed") {
		t.Fatalf("err = %v, want invalid-speed error", err)
	}
}

// TestReplayWhileSessionLocked: replay opens without the advisory lock, so
// a session a live mixi still holds open can be inspected.
func TestReplayWhileSessionLocked(t *testing.T) {
	raw, err := os.ReadFile(replayFixture)
	if err != nil {
		t.Fatal(err)
	}
	path := t.TempDir() + "/live_session.jsonl"
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	live, err := session.OpenJSONL(path, session.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close()

	var out bytes.Buffer
	if err := RunReplay(path, ReplayOptions{Speed: "instant", Out: &out}); err != nil {
		t.Fatalf("replay against a locked session: %v", err)
	}
	if !strings.Contains(out.String(), "Added the --version flag.") {
		t.Errorf("transcript truncated:\n%s", out.String())
	}
}

// TestPacerScalesAndCaps: recorded gaps shrink with speed and clamp at the
// cap; instant never sleeps.
func TestPacerScalesAndCaps(t *testing.T) {
	base := time.Unix(1000, 0)
	cases := []struct {
		speed string
		gap   time.Duration
		want  time.Duration
	}{
		{"instant", time.Hour, 0},
		{"5x", time.Second, 200 * time.Millisecond},
		{"1x", time.Hour, maxReplayGap},
	}
	for _, c := range cases {
		p, err := parseSpeed(c.speed)
		if err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		p.sleep(base, base.Add(c.gap))
		got := time.Since(start)
		if got < c.want || got > c.want+500*time.Millisecond {
			t.Errorf("speed %s gap %v: slept %v, want ≈%v", c.speed, c.gap, got, c.want)
		}
	}
}
