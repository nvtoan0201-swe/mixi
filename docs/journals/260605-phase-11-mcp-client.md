# Phase 11: MCP Client

**Date**: 2026-06-05 14:12
**Severity**: High (closes biggest functional gap — runtime tool discovery from external servers)
**Component**: internal/mcp (11 source + 5 test files, ~1.1k LOC), internal/wire (JSONL framing, ~200 LOC), cmd/mixi (MCP wiring), internal/tui (/mcp status)
**Status**: Resolved

## What Happened

Shipped Phase 11 "MCP Client" (commits 461c20e + a598b1b) — MCP protocol v2025-06-18 with fallback negotiation, stdio subprocess transport with process-group lifecycle (Setpgid, SIGTERM→SIGKILL escalation), JSON-RPC client with id-tracked pending-channel routing, manager state machine (CONFIGURED→INITIALIZING→READY→RESTARTING→FAILED with 1s/2s/4s backoff, 3 strikes), tool adapter with `mcp__server__tool` namespacing and schema passthrough, crash-mid-call → LLM-visible retry message. Tests: wire 71.8% coverage, mcp 86.6% coverage, chaos suite (crash/restart/fallback/3-strike) ×3 zero flakes, e2e ×4 (deny/allow/no-mcp/echo) green, full repo `-race` green (530+ tests). Code review found one flaky test (code-side race), two medium issues (write deadline, dead lock), zero blockers. Gates: tester DONE, code-reviewer DONE_WITH_CONCERNS → 4 findings fixed + re-verified ×10. Conformance tested live against `modelcontextprotocol/everything-server`: 13 tools listed, echo round-trip confirmed.

## The Brutal Truth

Building MCP client plumbing exposed three real bugs in isolation that looked fine under baseline testing, only surfaced under concurrent chaos or careful code review. The worst one stole ~2 hours: we thought we were routing real errors through the pending channel, but a synthetically-generated RPCError meant the sentinel `ErrConnClosed` was lost.

The first bug was in the chaos test itself. When the fake server crashes during a tool call, we synthesize an RPCError with the message "connection closed" and inject it into the routing path. But the `callTool` handler checks `errors.Is(err, ErrConnClosed)` to detect the crash and produce the LLM-visible "MCP server X crashed. It is restarting; retry the tool if needed." message. The problem: the synthesized RPCError was *not* the sentinel error, it was a fresh error wrapping it. So `errors.Is` returned false, the retry message never fired, and the LLM saw a generic protocol error instead. This is not a test bug — it's a production bug that the chaos test failed to catch. The fix: route a *real* `ErrConnClosed` through the pending channel instead of synthesizing RPCError at the call site. Now the sentinel travels as an error, `errors.Is` works, and the retry message fires. **Lesson: sentinel errors must travel as errors, not be re-encoded as protocol payloads.**

The second bug was a race in the `fail()` method. When a server transitions to FAILED (e.g., after 3 strikes or a spawn error), we notify any pending goroutines by calling `m.notify(msg)` *after* we've already called `setState(FAILED)` and `settle()`. The problem: `waitState` observers return on seeing FAILED, then immediately try to read the `notices` channel. But `notify` hasn't appended to it yet — a concurrent observer might wake on FAILED before the notice is queued. This was deeply flaky: it happened only under full-suite CPU load, passed 10/10 in isolation, and passed `-race` because the race is in the test itself (WaitFor hangs waiting for a message that was queued but consumed by a prior WaitFor call). The real fix: call `notify` **before** you flip the state, not after. Now any observer of FAILED is guaranteed to have seen the notice already. Code-side fix is more robust than test-only fixes — it tightens the invariant and hardens both test observers and production shutdown paths.

The third bug was subtle concurrency gap. The stdio Send method checks `ctx.Err()` once at entry, then calls `WriteLine` which blocks on the pipe with no deadline. Pipes don't implement `SetWriteDeadline`, so if a server stops reading stdin (alive-but-stuck), a Send will block indefinitely. The per-call timeout in `client.call` only governs the *response* wait, not the Send. This is narrow (requires a broken server), but unbounded. The fix: arm a write deadline on the pipe or wrap Send in a goroutine that respects context. We chose write deadline as the simpler option. **Lesson: when blocking on a subprocess resource, you need both context cancellation *and* a timeout-aware wrapper if the resource doesn't implement context natively.**

The fourth bug was dead code. Manager declared a `mu sync.Mutex` field that was never taken — `servers` is written only once at construction, then read-only, so mutex protection was phantom. Low severity, but it's a sign of incomplete design review. We removed it, and it's a good reminder to prune speculative synchronization. **Lesson: don't declare locks "just in case." If there's no writer after construction, there's no need for the lock.**

The fifth issue was schema coercion carry-forward from phase 3. The MCP spec allows arbitrary JSON schema in inputSchema; some servers use `$ref`, `patternProperties`, `additionalProperties` as schemas (not just constraints). Our coercion system from phase 3 skips these constructs because they're hard to simplify for the LLM. Decision: fall back to permissive `{"type":"object"}` with a WARN, and trust the server to validate authoritatively. This degrades UX (no input hints) but doesn't break — the server rejects bad data. User approved no extension to coercion, so schema complexity stays server-side.

## Technical Details

**internal/wire/jsonl.go (200 LOC + tests):** Reader reassembles lines under 1MiB cap using bufio.Scanner; over-long lines are discarded to the next message boundary. Writer holds a mutex and writes each message atomically; optional `deadline` parameter allows stdio Send to arm a write timeout. Shared with phases 12 (Extension Host) and 14 (RPC).

**internal/mcp/protocol.go (~150 LOC):** Request, Response, Notification, RPCError, Tool structs with JSON marshaling. Protocol version pinned to 2025-06-18 with fallback to 2025-03-26 during Initialize handshake. Unknown fields preserved via `json.RawMessage`.

**internal/mcp/transport.go (30 LOC):** Transport interface: `Send(ctx, msg)`, `Receive(ctx) (*Response, error)`, `Close()`, `Closed()`. Three implementations: stdio (subprocess), fake (test), nil (disabled).

**internal/mcp/stdio.go (120 LOC):** Spawns server with `cmd.SysProcAttr.Setpgid = true` (process group isolation), pipes stdin/stdout/stderr. stderr → DEBUG logs (raw, no prefixing). Send calls `wire.Write` with optional deadline. Receive calls `wire.Read` with per-message timeout. Close sends shutdown notification to stdin, waits 2s, sends SIGTERM to process group, waits 2s, escalates to SIGKILL. Cleanup deferred via `sync.Once`. Tests: kill -9 mid-read, stuck server, malformed output, graceful shutdown.

**internal/mcp/client.go (240 LOC):** Atomic id counter, `pending map[int64]chan *Response` (buffered(1), delete-under-lock). Handshake: send Initialize request with clientInfo + capabilities, receive Initialized notification. Tools/list: cached last-known tool catalog; updated on tools/list_changed notification (spawns a goroutine to re-list, avoids reader-goroutine reentrancy deadlock). Tools/call: route pending id, wait with per-call timeout (30s default, configurable), handle timeout by failing pending and stopping the connection. readLoop goroutine routes responses by id, handles notifications, logs unknown messages. Crash mid-call produces `ErrConnClosed` sentinel routed through pending (not synthetic RPCError).

**internal/mcp/manager.go (180 LOC):** State machine: CONFIGURED → INITIALIZING → READY → RESTARTING (1s/2s/4s backoff) → FAILED / CLOSED. Three consecutive transition failures (spawn error, handshake timeout, crash) → FAILED + user notice ("Server X failed to start. Use '/mcp reconnect X' to retry.") + tools error as unavailable. Strikes reset after 30s stable uptime. Close cancels context, waits `wg`, escalates stdio shutdown. Tools registered/deregistered on successful list or removed from catalog (don't churn the LLM's tool list mid-run; unavailable tools error instead).

**internal/mcp/tooladapter.go (120 LOC):** Adapter wraps client.Tool as `tools.Tool`. Name: `mcp__<sanitized-server>__<toolname>` (alphanumeric + hyphen-underscore, sanitize collisions to underscore). Description: `[mcp:server] <original>`. Call: map MCP ToolResult.Content to `[]ai.Content` (text → Text, image → Image, blob → base64 data URI; unknown types JSON-stringified + WARN). If server crashes mid-call, return IsError result with retry message. Unavailable tools return error: "Tool no longer provided by MCP server X."

**internal/mcp/manager_test.go (150 LOC):** chaos_test spawns fake server, calls tool, kills it mid-response, expects ErrConnClosed visible in result, restarts server, second call succeeds. fairAllFail test: inject crash, observe strike increment, call fails with correct error, server respawns. 3-strike test: three crashes in succession → FAILED state + notice channel signaled.

**internal/mcp/fake_server_test.go (100 LOC):** Test fake that responds to Initialize, lists two tools (echo + crash), echo round-trips args, crash tool exits (simulates kill -9). Malformed-response variant, timeout variant.

**internal/mcp/testdata/fake_mcp_server.go (200 LOC):** Standalone Go binary (go run main.go) that reads MCP requests, writes responses, supports `crash` command (exit(1) mid-tool). Compiled and invoked in integration tests (tag `mcp_integration`). 

**cmd/mixi/main.go (updated):** `startMCP` function: decode `mcpServers` config, spawn managers for each, wait initial handshakes (bounded by per-server timeout, default 5s) so tools reach system prompt before the first call. `--no-mcp` flag disables all servers. `agentNotifier.notice` carries MCP notices (crashes, recoveries) onto the agent event bus (EvNotice).

**internal/tui/app.go (updated):** `/mcp` command shows server states (READY/RESTARTING/FAILED + tool counts) and `/mcp reconnect <name>` action triggers manual reset of a failed server.

**tools/registry.go (contract change):** Mutex upgraded to RWMutex. `Defs()`, `All()`, `Get()` take RLock (no mutation). `Register()` takes Lock. Copies returned from `Defs()` and `All()` to prevent escape of internal slice. This allows MCP managers to register tools after startup; late-registered tools appear next turn via per-turn `Defs()` call in loop.go. No caller was assuming immutability; contract change is safe.

**cmd/mixi/run_tui.go (signature change):** `runTUI(ctx, sess, rc, cwd, asker, mcpMgr)` — new `mcpMgr` parameter. Wiring: if mcpMgr != nil (guarded typed-nil check), add to `tui.Deps`. Prevents typed-nil-in-interface trap.

## What We Tried

1. **Synthesize RPCError for crash-mid-call:** Catch connection close in the client, create RPCError("connection closed"), route through pending. Problem: `errors.Is(err, ErrConnClosed)` returns false; sentinel is lost. Fixed by: routing real `ErrConnClosed` sentinel error through the pending channel, not re-encoding it as protocol.

2. **notify() after setState():** Call `m.notify(msg)` after state transition. Problem: observer wakes on FAILED before notice is queued; flaky under load. Fixed by: notify before state flip, so invariant "observer sees FAILED implies notice is queued" holds.

3. **Unbounded stdin Send on stuck server:** Send checks context at entry, then blocks on pipe write indefinitely. Problem: per-call timeout only covers response, not Send. Fixed by: arm write deadline on the pipe in stdio.Send.

4. **Manager.mu unused:** Declared for future use. Problem: phantom synchronization, YAGNI. Fixed by: remove the field; servers map is write-once, safe to read without lock.

5. **MCP inputSchema coercion full depth:** Try to simplify arbitrary schemas (with `$ref`, `patternProperties`, etc.). Problem: hard to simplify correctly; schemas are diverse. Fixed by: fallback to permissive `{"type":"object"}` + WARN, server validates authoritatively.

6. **Conformance against fake server only:** Test only against synthetic fake. Problem: real protocol drift (version mismatch, field name differences) missed. Fixed by: live run against `modelcontextprotocol/everything-server` (13 tools, echo round-trip).

## Root Cause Analysis

**Sentinel error re-encoding:** We synthesized RPCError as a convenience at the call site, not realizing that `errors.Is` checks the error type directly, not the error message. Sentinel errors are *not* just markers; they're real error values that must travel as errors, not payloads. Root: insufficient familiarity with Go's error-wrapping patterns in this codebase; didn't ask "what will `errors.Is` see?"

**Notify-after-state race:** The state flip and notify were written as sequential steps, but concurrent observers can be scheduled between them. The pattern "set state, then notify dependents" is inherently racy. Root: didn't think deeply about the invariant ("observer sees FAILED implies it has the notice"). Should have written the invariant first, then checked if the code guarantees it.

**Unbounded Send on stuck server:** Pipes don't implement `SetWriteDeadline`; context cancellation doesn't interrupt a blocking write. Each blocking operation needs its own timeout if the underlying resource doesn't implement context. Root: wrote Send assuming context would be enough. Didn't trace the actual blocking call (pipe.Write) through to see it ignores context.

**Dead lock field:** Copied patterns from other sync primitives without asking "do I actually need this?" Speculative code is extra cognitive load and a source of latent bugs. Root: didn't review the manager initialization to confirm write-once-then-read.

**Schema complexity fallback:** We expected schemas to be simple (constraint keywords); some servers use advanced features as schemas (`additionalProperties: {type: "string"}`). Root: didn't read enough real MCP server schemas before designing coercion. The fallback (permissive + WARN) is pragmatic; coercion wasn't extended.

## Lessons Learned

1. **Sentinel errors must travel as errors, not payloads.** If you have a sentinel error that the consumer checks with `errors.Is`, make sure it travels through the system as an error value, not re-encoded as part of a message. Synthetic errors break the sentinel check.

2. **Write the invariant first, then check the code.** "Observer sees FAILED implies it has received the notice" is the invariant. Once you've stated it, the bug jumps out: notify-after-state violates it. Always state concurrency invariants in comments before the code.

3. **Context cancellation ≠ all timeouts.** Context cancels goroutines, not blocking system calls. If you're writing to a pipe, you need a write deadline *in addition to* context cancellation. The context can be cancelled, but the Write call doesn't know about it.

4. **Prune speculative code.** `mu sync.Mutex` declared "just in case" is technical debt. If there's no write after construction, there's no race. Keep the code surface clean; add synchronization when you need it, not before.

5. **Real conformance testing beats synthetic testing.** The fake server tests everything technically correct but misses protocol drift. Running against a real server (everything-server) confirmed protocol pinning actually works and the handshake negotiation flow is correct.

6. **Fall back gracefully when you don't fully understand a domain.** MCP inputSchemas can be arbitrarily complex. Instead of trying to simplify everything (and inevitably missing cases), fall back to a permissive schema and let the server validate. Degraded UX is better than broken.

7. **Process group isolation is essential for subprocess control.** Setpgid + kill(-pid) ensures the whole process tree dies, not just the parent. SIGTERM→SIGKILL escalation gives cleanup time without leaving zombies. This is subprocess 101, but easy to forget.

8. **Test under chaos, not just happy path.** The crash-mid-call test caught the sentinel error bug and the race in manager.fail(). Baseline tests wouldn't have. Chaos = kill, crash, restart, then verify recovery. Always stress-test concurrent primitives.

## Next Steps

1. **Phase 12 (Extension Host):** Reuses `internal/wire` JSONL framing for Extension Protocol (different semantics, same framing). Builds on phase 11's subprocess lifecycle patterns.

2. **Phase 13 (Structured Logging):** Wire slog to capture MCP lifecycle events (spawn, initialize, crash, restart, notice), tool calls with schemas/args/results. Replay log for debugging server issues.

3. **Phase 14 (RPC Mode):** Reuses `internal/wire` for client↔server JSON-RPC (different messages, same framing). Reuses `perm.NotifyAsker` pattern for remote asker. Sessions grant cross-RPC (signed once, trusted always).

4. **Phase 15:** Depends on 12/13 — refine based on observability and extension protocol shape.

5. **Phase 16 (Finalize):** Sessions, configuration, docs, release.

---

**Status:** DONE
**Summary:** Phase 11 shipped MCP client (protocol 2025-06-18/fallback, stdio transport with process-group lifecycle, JSON-RPC routing, manager state machine with 1s/2s/4s backoff + 3-strike disable, tool adapter with namespacing + crash-mid-call → retry message). Five bugs found and fixed: sentinel error re-encoding lost ErrConnClosed (chaos test caught), notify-after-state race (flaky 3-strike test symptom, fixed code-side), unbounded stdin Send (write deadline added), dead lock field (removed), schema complexity fallback (permissive + WARN approved). Conformance tested live: 13 tools from everything-server, echo round-trip verified. Wire 71.8%, MCP 86.6% coverage, zero flakes ×3, full repo `-race` green. `/home/student/mixi-agent/internal/mcp/`, `/home/student/mixi-agent/internal/wire/`, commit 461c20e + a598b1b.
