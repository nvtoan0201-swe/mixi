# Project Changelog

## Phase 1: AI Core Types & Streaming (Commit add2cf0)
**Date:** June 3, 2025

- Implement AI layer with normalized type system: sealed unions for Content (text/image/thinking/tool), Message (user/assistant/toolResult), StreamEvent
- Define Provider interface and global registry (Register, Resolve)
- Add Model type + known models (claude-3.5-sonnet, gpt-4, etc.)
- Implement JSON marshal/unmarshal with discriminators for wire protocol
- Build SSE (Server-Sent Events) decoder for streaming responses
- Add partial JSON repair for in-flight tool-call argument parsing
- Create stream event order contract (EventStart, Delta, End, Done/Error terminal)
- 14 tests, all passing

## Phase 2: Anthropic Streaming Provider (Commit 3ff6ddd)
**Date:** June 3, 2025

- Implement Anthropic API provider conforming to Provider interface
- Add block tracker for streaming content block indices
- Convert Anthropic wire format to normalized StreamEvent sequence
- Support extended thinking (redacted_thinking flag, signature preservation)
- Handle text streaming, tool-use (tool calls), and thinking blocks
- Implement request builder for model calls (RequestMessage)
- Add retry hooks (transient vs fatal error classification)
- Add API key hook for per-call credential resolution
- 15 tests including provider round-trip and live integration points, all passing

## Phase 3: JSON-Schema Tool-Arg Validation (Commit 17b7075)
**Date:** June 4, 2025

- Add schema validation: compile JSON Schema to runtime validator
- Implement type coercion (string→number, null→empty string, object unset on type mismatch)
- Build LLM-readable error formatter (no internal stack traces, field + constraint focus)
- Create tool argument validation in execution path (pre-Execute)
- Add validation tests: coerce edge cases, error messages, schema compilation
- 6 tests, all passing

## Phase 4: Agent Runtime Loop & Tools Interface (Commit b0ae628, 2026-06-03)

### Agent Runtime (`internal/agent`)
- Implement two-level agent loop: outer (follow-ups extend run) and inner (stream turn, dispatch tools, retry)
- Add Agent type and Config (model, tools registry, hooks, system prompt, max-turns)
- Implement Prompt/Continue/Subscribe/Abort public API
- Create message queues: steering (inject mid-turn), follow-up (extend after completion), bounded capacity
- Build retry logic: classify errors (transient vs fatal), exponential backoff with jitter, retry-after honor
- Add event bus: multiplex subscribers, drop render events on overflow, disconnect slow subscribers
- Implement event order contract (EvAgentStart → turns → EvAgentEnd, with turn/tool/message/retry sub-events)
- Create AgentMessage sealed union (ModelMessage, BashExecution, CompactionSummary, BranchSummary)
- Build system prompt assembler (tool definitions, context files, ordered injection)
- Add hook system: TransformContext, GetAPIKey, BeforeToolCall, AfterToolCall, ShouldStop
- 34 tests: agent lifecycle, queue limits, retries, hooks, events, streaming, concurrency; all passing

### Tools Interface (`internal/tools`)
- Define Tool interface: Name, Description, Schema (JSON), Mode (Parallel/Sequential), Execute (with streaming updates)
- Create Registry (name-keyed, build-once, read-safe)
- Add ToolResult type: content blocks + metadata + error flag
- Implement tool execution dispatch: parallel (goroutines) vs sequential (one at a time, source order)
- Add execution batching: any ExecSequential tool forces whole batch sequential
- Build panic recovery: recover panics → error result, emit tool events, continue run
- Add tool updates: streaming tools can emit partial output (non-blocking via select+default)
- 10 tests: execution order, parallel/sequential dispatch, panic recovery, unknown tool, updates; all passing

### Test Infrastructure
- Create agenttest package with FakeProvider for scripted streaming (test harness)
- Add test helpers for event stream validation
- Implement race detector green, goleak green

**Summary:** 44 tests in Phase 4 (agent + tools), 68+ total tests across all phases. Runtime loop fully functional: multi-turn conversations, parallel/sequential tool execution, steering/follow-up queues, retries with backoff, event streaming to subscribers, hook extensibility.

### Test Results
```
ok  github.com/user/mixi-agent/internal/agent       2.2s  (34 tests)
ok  github.com/user/mixi-agent/internal/tools       0.0s  (10 tests)
ok  github.com/user/mixi-agent/internal/schema      0.0s  (6 tests)
ok  github.com/user/mixi-agent/internal/ai          0.0s  (6 tests + subtests)
ok  github.com/user/mixi-agent/internal/ai/anthropic 0.0s  (7 tests)
ok  github.com/user/mixi-agent/internal/ai/sse      0.0s  (2 tests)
ok  github.com/user/mixi-agent/internal/ai/partialjson 0.0s (2 tests)
PASS: race detector, goleak
```

## Phase 5: Built-in Tools (Commit pending, today 2026-06-04)

### Nine Built-in Tools
- Implement `read` (file + image read; sniff PNG/JPEG/GIF/WebP, downscale >2000px)
- Implement `write` (atomic temp+rename, per-realpath mutex, optional fsync)
- Implement `edit` (fuzzy-match file, NFKC normalization, smart quotes/dashes/spaces table, whole-file rewrite on fuzzy, closest-line hint)
- Implement `bash` (Setpgid process groups, interleaved stdout+stderr, 100ms throttled updates, timeout SIGTERM→2s→SIGKILL)
- Implement `bash_output` (cursor reads from background job accumulator)
- Implement `kill_bash` (terminate background job)
- Implement `grep` (ripgrep wrapper, JSON-lines parse, rg required on PATH)
- Implement `find` (fd preferred + .gitignore respect; fallback: pure-Go WalkDir+doublestar+no-.gitignore note)
- Implement `ls` (pure-Go os.ReadDir)

### Shared Infrastructure
- Add `truncate.go`: head/tail truncation, UTF-8-safe boundaries
- Add `accumulator.go`: rolling-tail buffer (2×MaxBytes memory), lazy spill to temp file, cursor reads
- Add `job_table.go`: background job registry (b1, b2, ...), KillAll for agent shutdown
- Add `mutqueue.go`: per-realpath mutex pool, refcount cleanup
- Add `bintools.go`: lookPath wrapper, per-OS install-hint errors (no auto-download)
- Add `procgroup_unix.go` / `procgroup_windows.go`: Unix Setpgid group kill, Windows best-effort PID kill
- Add `read_image.go`: magic-byte sniff, downscale via x/image/draw
- Add `editmatch.go`: NFKC + smart-quote/dash/space normalization, exact→fuzzy match, Sørensen–Dice distance
- Add `edit_details.go`: UI metadata (diff, patch, firstChangedLine)

### Constants & Semantics
- Constants: MaxLines=2000, MaxBytes=50 KiB, GrepMaxLineLen=500, BashUpdateThrottle=100ms, BashDefaultTimeout=120s, BashMaxTimeout=600s
- Edit fuzzy-match: normalizes entire file; if any edit needs fuzzy, rewrites whole file
- Bash sequential: forces one-at-a-time execution to guarantee output ordering
- Process-group kill: Unix only (syscall.SysProcAttr{Setpgid}); Windows: best-effort PID kill
- Grep required: rg must be on PATH (install-hint error on absence)
- Find fallback: fd absent → pure-Go WalkDir+doublestar, outputs note "[fd not found: .gitignore not respected]"

### Test Results & Quality
- 20 source files + 13 test files in `internal/tools/` (builtins.go, tool.go, truncate.go, accumulator.go, job_table.go, mutqueue.go, bintools.go, procgroup_unix.go, procgroup_windows.go, read.go, read_image.go, write.go, edit.go, editmatch.go, edit_details.go, bash.go, bash_bg.go, grep.go, find.go, ls.go)
- 65 passing tests, 6 env-skips (rg/fd absent)
- 81.9% coverage, race detector green, goleak green
- Review fixes: Job.cursor race fix, exactly-limit truncation note, go mod tidy
- New deps: bmatcuk/doublestar/v4, golang.org/x/image (draw, webp)

All phases (1–5) delivered on schedule. Built-in tool suite complete for agentic file/command access. Foundation ready for permission/session/extension layers (Phase 6+).

## Phase 6: Session Persistence (2026-06-04)

### Session Storage Package (`internal/session`)
- Implement append-only JSONL tree storage with Header (v1) + 11 entry types
- Entry types: message[pinned], model_change, thinking_level_change, active_tools_change, compaction, branch_summary, custom, custom_message, label, session_info, leaf
- Type-discriminated codec delegating message payloads to ai.MarshalMessage/UnmarshalMessage
- Implement Storage interface with two impls: jsonlStore (disk) and memStore (tests/--no-save)
- Deferred first write: no file/lock until first message entry; empty sessions leave zero artifacts
- Add O_APPEND handle + optional fsync per message entry
- Implement file locking: flock(LOCK_EX|LOCK_NB) on Unix + LockFileEx on Windows (x/sys, untested); pid-hint sidecar
- Build Loader with stream-scan, 10 MiB line cap, crash-tail recovery (WARN + truncate partial trailing; never truncate without valid header); reject version>1 with upgrade hint
- Implement tree operations: byId index + leaf replay; PathToRoot, SetLeaf, CommonAncestor, fork (file-order prefix + parentSession header)
- Build Manager: ~/.mixi/sessions/<cwd-slug>/<ts>_<uuidv7>.jsonl; Create/Open/ContinueRecent(newest mtime)/Fork/InMemory
- Entry IDs: last-8-hex of uuidv7 (deliberate deviation from first-8 to avoid timestamp-prefix collisions); ≤100 retries; full-uuid fallback
- New deps: github.com/google/uuid, golang.org/x/sys (windows-only)

### Test Results & Quality
- 11 source files + 8 test files in `internal/session/`
- 38 passing tests, -race -count=5 stable
- 86.6% coverage across tools + session packages
- Windows cross-vet clean

**Summary:** Session storage layer complete. Multi-turn conversation history now persists to disk with crash recovery, branching support, and concurrent access safety. All six phases (1–5: agent runtime + built-in tools, 6: session persistence) on schedule.
