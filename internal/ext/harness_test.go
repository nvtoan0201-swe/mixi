//go:build unix

package ext

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/user/mixi-agent/internal/agent"
	"github.com/user/mixi-agent/internal/tools"
)

// fakeExtBin is the compiled scriptable extension; empty when the Go
// toolchain is unavailable at test time (subprocess tests then skip).
var fakeExtBin string

var fastBackoff = []time.Duration{time.Millisecond, 2 * time.Millisecond, 4 * time.Millisecond}

func TestMain(m *testing.M) {
	os.Exit(buildFakeExtensionAndRun(m))
}

func buildFakeExtensionAndRun(m *testing.M) int {
	if gobin, err := exec.LookPath("go"); err == nil {
		dir, err := os.MkdirTemp("", "ext-fake")
		if err == nil {
			defer os.RemoveAll(dir)
			bin := filepath.Join(dir, "fake_extension")
			cmd := exec.Command(gobin, "build", "-o", bin, "fake_extension.go")
			cmd.Dir = "testdata"
			if out, err := cmd.CombinedOutput(); err == nil {
				fakeExtBin = bin
			} else {
				fmt.Fprintf(os.Stderr, "ext: fake extension build failed (subprocess tests skip): %v\n%s\n", err, out)
			}
		}
	}
	return m.Run()
}

// recorderAPI captures every API call for assertions.
type recorderAPI struct {
	mu       sync.Mutex
	statuses map[string]string
	notices  []string
	messages []string // "deliverAs:content"
	entries  []string // "customType:data"
}

func newRecorderAPI() *recorderAPI {
	return &recorderAPI{statuses: map[string]string{}}
}

func (r *recorderAPI) SendUserMessage(content, deliverAs string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.messages = append(r.messages, deliverAs+":"+content)
	return nil
}

func (r *recorderAPI) AppendEntry(customType string, data json.RawMessage) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = append(r.entries, customType+":"+string(data))
	return nil
}

func (r *recorderAPI) SetStatus(key, text string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.statuses[key] = text
	return nil
}

func (r *recorderAPI) Notify(text, _ string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.notices = append(r.notices, text)
	return nil
}

func (r *recorderAPI) AskSelect(_ context.Context, _ string, options []string) (string, error) {
	return options[0], nil
}

// hostOpts tweaks the test host per scenario.
type hostOpts struct {
	env       map[string]string // merged into the single "fake" extension
	servers   map[string]Config // overrides the single-extension default
	notify    func(string)
	helloWait time.Duration
	blockTO   time.Duration
	backoff   []time.Duration
}

// startTestHost builds and starts a host over the fake extension binary.
func startTestHost(t *testing.T, mode string, o hostOpts) (*Host, *tools.Registry) {
	t.Helper()
	if fakeExtBin == "" {
		t.Skip("go toolchain unavailable — fake extension not built")
	}
	cfgs := o.servers
	if cfgs == nil {
		env := map[string]string{"FAKE_EXT_MODE": mode}
		for k, v := range o.env {
			env[k] = v
		}
		cfgs = map[string]Config{"fake": {Command: fakeExtBin, Env: env}}
	}
	reg := tools.NewRegistry()
	backoff := o.backoff
	if backoff == nil {
		backoff = fastBackoff
	}
	h, err := NewHost(Options{
		Configs:      cfgs,
		Registry:     reg,
		Notify:       o.notify,
		Ready:        ReadyInfo{SessionID: "s-test", Cwd: "/tmp", Mode: "print", Model: "faux/scripted"},
		Backoff:      backoff,
		StableAfter:  time.Hour, // strikes never reset unless a test wants it
		HelloWait:    orDefault(o.helloWait, 2*time.Second),
		BlockTimeout: orDefault(o.blockTO, 2*time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	h.Start(context.Background())
	t.Cleanup(h.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	h.WaitInitial(ctx)
	return h, reg
}

func orDefault(d, def time.Duration) time.Duration {
	if d <= 0 {
		return def
	}
	return d
}

// bindRecorder binds a recorder API plus a caller-fed event channel.
func bindRecorder(t *testing.T, h *Host) (*recorderAPI, chan agent.Event) {
	t.Helper()
	api := newRecorderAPI()
	events := make(chan agent.Event, 64)
	h.Bind(api, events)
	t.Cleanup(func() { close(events) })
	return api, events
}

// waitExtState polls until the named extension reaches the wanted state.
func waitExtState(t *testing.T, h *Host, name string, want ExtState) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		for _, st := range h.Status() {
			if st.Name == name && st.State == want {
				return
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("extension %q never reached %s; status %+v", name, want, h.Status())
}

// waitFor polls a condition with a shared deadline.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}
