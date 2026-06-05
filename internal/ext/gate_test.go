//go:build unix

package ext

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/user/mixi-agent/internal/ai"
)

func bashCall(cmd string) ai.ToolCall {
	args, _ := json.Marshal(map[string]string{"command": cmd})
	return ai.ToolCall{ID: "c1", Name: "bash", Args: args}
}

func TestGateBlocksFirstWins(t *testing.T) {
	h, _ := startTestHost(t, "blocker", hostOpts{env: map[string]string{"FAKE_EXT_BLOCK_TOOL": "bash"}})
	waitExtState(t, h, "fake", StateReady)

	args, block, err := h.FilterToolCall(context.Background(), bashCall("rm -rf /tmp/x"))
	if err != nil {
		t.Fatalf("gate must never fail closed: %v", err)
	}
	if block == nil || args != nil {
		t.Fatalf("expected block, got args=%s block=%v", args, block)
	}
	if want := "blocked by fake"; !strings.Contains(block.Reason, want) {
		t.Fatalf("reason %q misses %q", block.Reason, want)
	}

	// A tool the gate does not block passes through unchanged.
	call := ai.ToolCall{ID: "c2", Name: "read", Args: json.RawMessage(`{"path":"x"}`)}
	args, block, err = h.FilterToolCall(context.Background(), call)
	if err != nil || block != nil || args != nil {
		t.Fatalf("expected pass-through, got args=%s block=%v err=%v", args, block, err)
	}
}

func TestGateMutatesArgs(t *testing.T) {
	h, _ := startTestHost(t, "mutator", hostOpts{env: map[string]string{"FAKE_EXT_MUTATE": `{"command":"echo safe"}`}})
	waitExtState(t, h, "fake", StateReady)

	args, block, err := h.FilterToolCall(context.Background(), bashCall("rm -rf /"))
	if err != nil || block != nil {
		t.Fatalf("unexpected: block=%v err=%v", block, err)
	}
	if string(args) != `{"command":"echo safe"}` {
		t.Fatalf("args = %s", args)
	}
}

func TestGateTimeoutStrikesDemote(t *testing.T) {
	// The production budget is 5s (blockingTimeout); the seam shrinks it so
	// the test proves the mechanism: a hung extension delays a call by at
	// most the budget, and three timeouts demote it out of the gate path.
	const budget = 150 * time.Millisecond
	h, _ := startTestHost(t, "slow", hostOpts{blockTO: budget})
	waitExtState(t, h, "fake", StateReady)

	for i := 0; i < maxBlockStrikes; i++ {
		start := time.Now()
		_, block, err := h.FilterToolCall(context.Background(), bashCall("ls"))
		if err != nil || block != nil {
			t.Fatalf("timeout must be a no-op: block=%v err=%v", block, err)
		}
		if el := time.Since(start); el > 10*budget {
			t.Fatalf("gate stalled %s; budget %s", el, budget)
		}
	}
	waitExtState(t, h, "fake", StateDegraded)

	// Demoted: the gate skips it entirely now.
	start := time.Now()
	if _, block, _ := h.FilterToolCall(context.Background(), bashCall("ls")); block != nil {
		t.Fatal("demoted extension must not gate")
	}
	if el := time.Since(start); el > budget/2 {
		t.Fatalf("demoted gate still waited %s", el)
	}
}
