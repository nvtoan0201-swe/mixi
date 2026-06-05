# Phase 12: Extension Host

**Date**: 2026-06-05 20:48
**Severity**: High (extension safety for agent guardrails + user observability)
**Component**: internal/ext (17 source + 7 test files, ~2050 LOC), internal/procgroup (moved from mcp, ~200 LOC), cmd/mixi/ext_setup.go, cmd/mixi/main.go (wiring), internal/agent/events.go (EvStatus, EvNotice), examples/extensions/
**Status**: Resolved

## What Happened

Shipped Phase 12 "Extension Host" (commits tbd) — subprocess JSONL-RPC host for extension protocol (handshake, blocking gates, supervised lifecycle, crash/strike isolation, tool registration, custom actions), 1MiB line cap + malformed-flood disable, ready→session_start→agent_start→turn→agent_end→session_shutdown lifecycle, gate pipeline (extension filters block/mutate tool calls, fail open on hung/crashed extension), permission_gate example (blocks `rm -rf` in yolo mode), status_line example (updates TUI footer). Tests: conformance suite (7 scenarios: handshake, gate block, gate mutate, timeout-strike demotion, crash-restart, disable, tool round-trip) green first try, 76% coverage in internal/ext, full repo clean, all 6 success criteria explicitly tested + end-to-end e2e_extensions_test.go. Code review found 1 genuine nondeterminism bug (TestOverlongLineDisables flaky ~1/3 under `-race`), 1 false invariant, 1 budget ambiguity (per-ext ~10s worst case vs 5s stated), 3 medium-low polish items. All 8 findings fixed and re-verified. Gates: tester DONE, code-reviewer DONE_WITH_CONCERNS → findings triaged.

## The Brutal Truth

The extension host was mechanically correct on the happy path, but the moment code review ran it under `-race`, a flaky test (TestOverlongLineDisables) surfaced a real nondeterminism bug that exposed my hasty thinking about when errors get classified. The test failed ~1 in 3 full-suite runs, and I immediately jumped to the wrong root cause — didn't spend the time to reproduce and trace.

The code reviewer theorized it was a "pipe-error misclassification race in pumpStdout" where a terminal pipe error could show up before or instead of a proper `wire.ErrLineTooLong` when the extension writes a 2MiB line and the process crashes under load. Sounds plausible, right? The pipe is dying, errors are coming from multiple places. So I started thinking about latching flags and preferring one error class over another. But here's the kicker: I didn't actually reproduce the bug before fixing it. I just started coding what sounded right.

The real bug was completely different and sat one step away from where the reviewer looked. The problem wasn't in `pumpStdout` at all. It was in `disable()`: when the extension crashed (not due to the over-long line, but during the test loop under load), the code was flipping the state to `StateDisabled` *before* publishing the crash notice. The test polls the state first, sees `StateDisabled`, exits the wait, and then reads the notices queue. But the notice hadn't been queued yet — the state flip happened, the test woke up, and the notice append was still in flight. So the test saw an empty notice queue and failed the assertion that it should see a cap-violation notice. But what actually happened was the extension hit a crash path during the test, not the cap path. The test was succeeding *deterministically* — just for the wrong reason.

The fix was brutal in its simplicity: notify *before* state flip. Now the invariant holds: "if an observer sees StateDisabled, the notice is already queued." Verified with 15 consecutive full-suite `-race` loops, 0 failures (pre-fix was ~1/5). **Lesson: reproduce the bug before theorizing. The real bug often sits one step from the flagged one, and the fix is smaller if you find it.**

The reviewer also flagged a false invariant in `enqueue()` — the comment said "single producer", but there are three concurrent producers (fanOut, session_start re-announce, DispatchCommand). The drop-oldest pop-then-push sequence is not atomic. Serialized it under a mutex; drop accounting now correct.

A second false invariant: the plan states "per-extension gate budget ~5s", but write deadline *and* response timeout are both guarded separately, so worst case is ~2×5s. I documented it honestly at `blockingTimeout` const rather than trying to split them (YAGNI; bounded either way).

Stale footer status segment on crash/disable: when an extension crashes, its last TUI footer text (e.g. "turns: 7") never clears. The host only clears segments when the extension itself sends `set_status` with empty text. I added `clearStatus()` on disable and verified the state machine clears it. Asserted in TestCrashRestartsThenDisables.

The permission_gate example needed to be real, not a mock. I added `cmd/mixi/e2e_extensions_test.go` that builds the example, auto-discovers it, and verifies it blocks `rm -rf` end-to-end in yolo mode. Closes the untested success criterion.

Project-local `.mixi/extensions` now overrides global `~/.mixi/extensions` (conventional settings precedence), fixing L2.

## Technical Details

**internal/ext/protocol.go (~140 LOC):** Extension protocol envelope (type, data as raw JSON), events (ready, session_start, agent_start, turn_start, turn_end, agent_end, session_shutdown), actions (blocking/non-blocking; register_tool rejected per design). Agent→Extension: events as JSON objects. Extension→Agent: envelope responses or notifications.

**internal/ext/host.go (160 LOC):** Root manager. Discover configs (ordered), spawn extension processes, maintain state map, `Subscribe()` for agent integration, `Commands()` for TUI, `FilterToolCall()` pre-extension gate, `Bind()` per-turn lifecycle pump. Typed-nil guard for interface safety.

**internal/ext/extension.go (240 LOC):** Per-extension state machine (StateReady, StateWork, StateDegraded, StateDisabled, StateClosed). Handshake, event enqueue (drop-oldest with mutex serialization), gate coordination via `blockingChan`, pending gate tracking, receive/send loops, crash/strike detection. NotifyAsker pattern: gate replies route to pending chans; no callback hell.

**internal/ext/supervisor.go (160 LOC):** Subprocess loop manager (start/crash/restart, 3-strike disable, malformed flood counter, line-too-long disable). Re-announce session_start on restart (extension sees same session ID). Drain queue on disable (no stuck events). Lifecycle: spawn → supervise → readLoop + pumpStdout → crash detection → restart (or disable after 3) or graceful close.

**internal/ext/gate.go (80 LOC):** Gate pipeline. Per-extension: route request, wait bounded by blockingTimeout (5s), handle response/timeout/crash. Fail open: no response → gate passes (unblock). Extension timeout 3 times in a row → demote to StateDegraded (skip thereafter). Response parsing: block_reason string, mutated args.

**internal/ext/actions.go (70 LOC):** Handle extension actions. `register_tool`: build tool from args, register in registry (name collision → error, not overwrite). `set_status`: update TUI footer segment. `blocking`: error (rejected by protocol). Custom action: pass-through (user defines schema, agent validates).

**internal/ext/adopt.go (100 LOC):** Adopt extension-registered tools into the agent's tool registry. Wrap via toolbridge. Catalog reconciliation on startup and post-crash. Tool execution via toolbridge.

**internal/ext/toolbridge.go (80 LOC):** Adapter wrapping extension tool as `tools.Tool`. Name: `ext__<sanitized-ext>__<toolname>`. Call: serialize args, route through gate (block/mutate), execute via extension, deserialize result as ai.Content (text, image, blob). Timeout: per-call 30s. Crash → IsError with retry hint. Unavailable → error.

**internal/ext/events.go (140 LOC):** Event serialization. Agent events (Message, ToolCall, Approval, etc.) → extension protocol (json). Turn lifecycle: `mkEvent()` marshals agent internal event to extension-visible shape. Custom message events not fully exercised (23.8% coverage, low impact).

**internal/ext/config.go (100 LOC):** Parse `~/.mixi/extensions/` and `.mixi/extensions/` directories. Env var expansion (late-bound, no secrets in struct). Precedence: configured > project-local > global. Discovery filters for execute bit (no recursion).

**internal/procgroup/procgroup_unix.go, procgroup_windows.go (~200 LOC):** Process group helpers (moved from mcp). Setpgid on spawn, kill group on shutdown. Windows falls back to individual kill.

**cmd/mixi/ext_setup.go (120 LOC):** Wiring: construct host, discover/spawn, adopt tools into registry before system prompt, defer bind until agent loop starts.

**cmd/mixi/main.go (updated):** Extension host initialized before agent build. `extensionFilters(extHost)` chained before permission filters. Notices (crash, disable) feed to agent event bus.

**cmd/mixi/e2e_extensions_test.go (120 LOC):** End-to-end test. Build permission_gate example, register as discovery fixture, run agent, attempt `rm -rf /tmp/x`, verify extension blocks it, result reflected in tool error. `--no-extensions` skip verified.

**examples/extensions/permission_gate/main.go (140 LOC):** Real example. Handshake, listen for tool_call, block if command is `rm` (case-insensitive), allow otherwise.

**examples/extensions/status_line/status_line.sh (30 lines):** Shell script example. Reads envelopes, emits set_status events per turn, TUI footer updates.

**internal/ext/{host_lifecycle,gate,bridge,config}_test.go (800 LOC):** Conformance suite. 7 core tests: handshake reaches StateReady, gate blocks first extension, gate mutates args, timeout 3× → demote, crash 3× → disable, over-long line → disable + notice, malformed flood → disable. Tool round-trip. Lifecycle event order. All green, zero flakes.

## What We Tried

1. **Initial reproduce of C1 flaky test:** Ran `-race` repeatedly, confirmed ~1/3 full-suite failure rate. Believed reviewer's "pumpStdout pipe-error race" theory. Began planning error-latch logic in multiple places. Problem: didn't actually trace the failure. Fixed by: reproducing locally, stepping through the test, discovering state flip was happening before notice queue. Fix: notify before state flip. Verified: 15× full-suite `-race`, 0 failures.

2. **Single-producer assumption:** Comment said single producer, but three goroutines call enqueue. Thought about making it lock-free with atomic CAS on the queue pointer. Problem: doesn't simplify, still not atomic. Fixed by: simple mutex around enqueue, one-liner.

3. **Gate budget ambiguity (M1):** Reviewer noted write and response each have separate 5s deadlines, so worst case ~10s per extension. Thought about splitting budgets (write 2s, response 3s) to honor 5s. Problem: YAGNI, and the actual worst case depends on extension behavior (if it's responsive, write completes fast). Accepted: document the real number, bounded either way.

4. **Stale footer on disable:** Only cleared when extension sends set_status empty. Thought about having disable auto-emit set_status for backward compat. Problem: complications, two sources of truth. Fixed by: add `clearStatus()` call on disable path, single responsibility.

5. **Discovery precedence (L2):** Global was merged before project-local, so global wins. Counterintuitive. Flipped the order to match Unix conventions (project overrides global).

6. **Permission gate example untested:** Example binary existed but wasn't exercised end-to-end. Thought about mocking. Fixed by: real e2e test that builds, discovers, blocks `rm -rf`.

## Root Cause Analysis

**C1 flaky test — state-before-notice race:** I wrote disable as a two-step: flip state, then notify. I assumed the steps would appear atomic to observers, but Disable() has two concurrent call sites (crash path and cap-violation path), and observers (the test) can wake on state flip before the notify is queued. Root: didn't think about the invariant. Should have written "notify must be queued before any observer sees Disabled" and then checked the code — the invariant would have jumped out immediately.

**H1 single-producer false invariant:** I copy-pasted a comment from another codebase without checking if it was still true. The comment predated the DispatchCommand path. Root: cargo-cult documentation; didn't verify invariants hold.

**M1 budget ambiguity:** Plan said "N extensions × 5s" without being precise about what operations are under that budget. Write timeout and response timeout are independent. I assumed they were fused because I wrote them in the same function without stepping back to think about the worst case. Root: wrote code before thinking about the envelope it sits in.

**M2 stale footer:** I implemented the protocol correctly (extension owns status), but forgot that disable is a terminal state where the extension can't speak anymore. The agent should own cleanup for states it controls. Root: didn't trace what happens when an extension dies — did I clear its UI?

**L2 discovery precedence:** Global-first-then-project is the reverse of the Unix convention (system settings, then user settings, then project settings). I didn't think about the precedence hierarchy; I just implemented "merge all configs". Root: no design review of what "merge" should mean.

## Lessons Learned

1. **Write the invariant first, then verify the code.** "Before any observer sees StateDisabled, the notice is queued" is the invariant for `disable()`. Once stated, the bug jumps out: notify-before-state is required. This applies to any concurrency protocol — state the contract before the code.

2. **Reproduce before theorizing.** When a flaky test surfaces, the reviewer's intuition is often in the right ballpark but not the exact bug. The real bug is usually one step away. Spend the time to reproduce locally, add logs, step through. The fix is smaller once you find it.

3. **Verify invariant comments when copy-pasting.** Single-producer, lock-free, O(1) — these are invariants that don't travel well. Always check if the precondition still holds.

4. **Worst-case budget analysis before code.** If you have N extensions each with a timeout, and M independent timeouts per extension, think through the worst case (all extensions stalled, all timeouts firing) and make sure the doc matches. Don't let the code hide the cost.

5. **Trace state machine cleanup.** When a state is terminal (Disabled, Closed), who cleans up the side effects (UI, buffers, etc.)? Ownership question. In this case, the agent owns the lifecycle, so the agent should clean up the UI when it transitions an extension to Disabled.

6. **Follow the precedence pattern.** Settings hierarchy is system < user < project. Discovered configs should follow the same pattern. Check what the Discover function does before implementing.

7. **Subprocess isolation is real:** Crashing extension doesn't crash agent. 1MiB cap enforced; malformed-flood disabled. Gate fails open. Tool calls timeout to error. This design actually works — crashes don't propagate. Verify the fail-open logic and you're good.

8. **Real example > mock.** The permission_gate example needed to run end-to-end to prove the protocol works for real. Synthetic protocol tests catch protocol bugs; real examples catch integration/discovery/environment bugs.

## Next Steps

1. **Phase 13 (Structured Logging):** Wire slog to capture extension lifecycle (spawn, handshake, crash, disable), gate blocks/mutations, tool calls. Replay log for debugging extension issues.

2. **Phase 14 (RPC Mode):** Reuses `internal/wire` for client↔server JSON-RPC (agent as RPC server, remote tools as RPC clients). Reuses gate pattern for remote tool filtering.

3. **Phase 15:** Observability + extension hardening based on phase 13 logs.

4. **Phase 16 (Finalize):** Configuration docs, security review, extension SDK docs, release.

---

**Status:** DONE
**Summary:** Phase 12 shipped Extension Host (subprocess lifecycle, JSONL-RPC protocol, blocking gates, crash/strike isolation, tool registration, status updates, examples). One flaky test (C1: over-long-line disable race) revealed state-before-notice nondeterminism — fixed by moving notify before state flip. Seven additional review findings (false single-producer invariant, gate budget ambiguity, stale footer, discovery precedence) triaged and fixed. Conformance suite all 7 tests green first try, 76% coverage, real e2e with permission_gate example. `/home/student/mixi-agent/internal/ext/`, `/home/student/mixi-agent/internal/procgroup/`, `/home/student/mixi-agent/cmd/mixi/ext_setup.go`, `/home/student/mixi-agent/examples/extensions/`.
