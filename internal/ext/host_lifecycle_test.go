//go:build unix

package ext

import (
	"strings"
	"sync"
	"testing"
	"time"
)

func TestHandshakeReachesReady(t *testing.T) {
	h, _ := startTestHost(t, "echo", hostOpts{})
	waitExtState(t, h, "fake", StateReady)
	st := h.Status()
	if len(st) != 1 || st[0].Name != "fake" || st[0].Tools != 0 {
		t.Fatalf("status = %+v", st)
	}
}

// noticeRecorder captures user notices thread-safely.
type noticeRecorder struct {
	mu    sync.Mutex
	texts []string
}

func (n *noticeRecorder) add(s string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.texts = append(n.texts, s)
}

func (n *noticeRecorder) joined() string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return strings.Join(n.texts, "\n")
}

func TestCrashRestartsThenDisables(t *testing.T) {
	var notices noticeRecorder
	h, _ := startTestHost(t, "crash", hostOpts{notify: notices.add})
	waitExtState(t, h, "fake", StateReady)

	// Binding announces session_start; the extension crashes on it. Each
	// restart re-announces session_start, so the crash loop self-sustains
	// until the strike policy disables the extension.
	api, _ := bindRecorder(t, h)
	waitExtState(t, h, "fake", StateDisabled)
	waitFor(t, "disable notice", func() bool {
		return strings.Contains(notices.joined(), "disabled")
	})
	// Disabling clears the extension's footer status segment.
	api.mu.Lock()
	text, cleared := api.statuses["fake"]
	api.mu.Unlock()
	if !cleared || text != "" {
		t.Fatalf("status segment not cleared: %q (set=%v)", text, cleared)
	}
}

func TestOverlongLineDisables(t *testing.T) {
	var notices noticeRecorder
	h, _ := startTestHost(t, "garbage", hostOpts{notify: notices.add})
	waitExtState(t, h, "fake", StateDisabled)
	if !strings.Contains(notices.joined(), "1MiB cap") {
		t.Fatalf("notices = %q", notices.joined())
	}
}

func TestMalformedFloodDisables(t *testing.T) {
	var notices noticeRecorder
	h, _ := startTestHost(t, "malformed", hostOpts{notify: notices.add})
	waitExtState(t, h, "fake", StateDisabled)
	if !strings.Contains(notices.joined(), "malformed") {
		t.Fatalf("notices = %q", notices.joined())
	}
}

func TestImmediateExitDisablesAfterStrikes(t *testing.T) {
	h, _ := startTestHost(t, "exit", hostOpts{})
	waitExtState(t, h, "fake", StateDisabled)
}

func TestNoHelloTimesOut(t *testing.T) {
	h, _ := startTestHost(t, "no-hello", hostOpts{
		helloWait: 100 * time.Millisecond,
		backoff:   []time.Duration{time.Millisecond},
	})
	waitExtState(t, h, "fake", StateDisabled)
}

func TestBadProtocolRejected(t *testing.T) {
	h, _ := startTestHost(t, "bad-proto", hostOpts{
		backoff: []time.Duration{time.Millisecond},
	})
	waitExtState(t, h, "fake", StateDisabled)
}
