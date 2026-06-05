# mixi-agent Codebase Summary

**Module:** `github.com/user/mixi-agent` | **Status:** Phase 13 complete (Observability & Replay)

## Package Overview

| Package | Purpose | Key Types |
|---------|---------|-----------|
| `cmd/mixi` | CLI entry point: flag parsing, settings precedence, session open, tool wiring, signal handling, mode dispatch | `Flags`, `RuntimeConfig`, `realMain`, signal handlers |
| `internal/ai` | Normalized type system for all providers; provider registry | `Content`, `Message`, `StreamEvent`, `Model`, `Provider` |
| `internal/ai/anthropic` | Anthropic API provider (SSE streaming, thinking, retries) | `Provider`, `BlockTracker`, SSE event mapping |
| `internal/ai/faux` | Scripted provider for E2E testing (always compiled); replays JSON script via `MIXI_FAUX_SCRIPT` env | `provider`, `Script`, `ScriptTurn` |
| `internal/ai/sse` | Server-Sent Events decoder | `Decoder` |
| `internal/ai/partialjson` | Streaming partial JSON repair | `RepairContext` |
| `internal/agent` | Two-level runtime loop: turns, tool dispatch, queues, retry, events | `Agent`, `runLoop`, `Event` (sealed union) |
| `internal/agent/agenttest` | Scripted test provider (fake stream generator) | `FakeProvider` |
| `internal/config` | Settings files + CLI flags with precedence resolution; runtime config assembly | `Settings`, `Flags`, `RuntimeConfig`, `Resolve` |
| `internal/modes` | Headless run modes (print now; RPC/replay stubbed); EventSink abstraction | `EventSink`, `TextSink`, `JSONSink`, `RunPrint` |
| `internal/obs` | Structured JSON logging (slog → ~/.mixi/logs/mixi-<date>.jsonl, daily rotation keep 7), usage tracking per turn (tokens, cost, model, latency), session replay with pacing (1x/5x/instant, entry-id truncation) | `Setup`, `Tracker`, `Snapshot`, `RunReplay`, `ReplayOptions` |
| `internal/schema` | JSON-Schema validation, coercion, LLM-readable errors | `Compile`, `Coerce`, `ErrFormatter` |
| `internal/tools` | Nine built-in tools (read, write, edit, bash, bash_output, kill_bash, grep, find, ls); shared infra (truncate, accumulator, job table, process group, binary lookup); Registry now concurrent-safe (RWMutex) for late MCP tool registration | `Tool`, `Registry`, `ToolResult`, `accumulator`, `JobTable` |
| `internal/session` | Append-only JSONL tree storage for conversations; file locking, crash recovery, branching | `Storage`, `Manager`, `Loader`, `Entry`, `Header` |
| `internal/compact` | Context compaction: usage-anchored token estimation, turn serialization, cut-point selection, LLM summarization, Compactor state machine | `Compactor`, `Controller`, `LLMSummarizer`, `Estimator` |
| `internal/workset` | Working-set assembly: file-freshness tracking, context budgeting (trim→compact→error), custom-entry persistence | `WorkingSet`, `FileStamp`, `ContextBuilder` |
| `internal/perm` | Permission engine: 4 modes (plan/prompt/auto-edit/yolo), rule globs (bash command, path doublestar, MCP name), baseline screens (denied bash patterns, secret-glob forced-ask, write outside cwd), session grants, headless asker, TUI asker | `Engine`, `Policy`, `Mode`, `Rule`, `Asker`, `HeadlessAsker`, `NotifyAsker`, `PendingAsk` |
| `internal/tui` | Bubble Tea interactive mode: event bridge, streaming transcript viewport, tool cards (with colorized diffs), approval modal, status bar, input editor (history ring + slash autocomplete + $EDITOR), keymap, slash commands | `App`, `model`, `bridge`, `transcript`, `msgview`, `toolview`, `approval`, `editor`, `statusbar`, `keymap` |
| `internal/wire` | JSONL framing (1MiB line cap, atomic writes, optional write deadline) — shared transport seam for subprocess extensions (phase 12) and RPC (phase 14) | `FrameWriter`, `FrameReader`, `MaxLineLen` |
| `internal/mcp` | MCP client for tool integration: protocol negotiation (2025-06-18 with 2025-03-26 fallback), stdio transport (Setpgid, stderr→DEBUG, SIGTERM→SIGKILL), id-tracked request routing, per-call timeouts (30s default), malformed-line rate-cap (10/min), tool adapter (names `mcp__<server>__<tool>`, schema passthrough w/ permissive fallback) | `Client`, `Manager`, `Transport`, `ServerConfig` |
| `internal/ext` | Extension host: subprocess JSONL-RPC server discovery (settings + executables in ~/.mixi/extensions/ and .mixi/extensions/), hello handshake (5s timeout), per-extension FIFO event queue (cap 256, drop-oldest), blocking tool_call gate (5s budget, 3-strike disable), action API (send_message, append_entry, set_status, notify, ask_select, register_command), crash isolation (restart backoff 1s/2s/4s), 1MiB wire cap violation or 10 malformed msgs/min → Disabled, protocol:1 experimental mark | `Host`, `Extension`, `Config`, `Protocol`, `Supervisor` |

## Architecture Layers

### Layer 0: CLI & Mode Dispatch (`cmd/mixi`, `internal/config`, `internal/modes`, `internal/tui`)
- **CLI entry point** (`main.go`): parses args, detects piped stdin, resolves config, opens session, dispatches to mode
- **Flag parsing** (`internal/config/flags.go`): stdlib flag.FlagSet with repeatable flags (--message, --allow, --deny), WasSet tracking for precedence
- **Settings merging** (`internal/config/config.go`): JSON load from ~/.mixi/settings.json, .mixi/settings.json (cwd), and --config file; deny lists append-only across layers; ${ENV} brace-only expansion
- **Precedence resolution** (`internal/config/runtime.go`): Resolve(flags, settings, cwd, stdin) → RuntimeConfig with flags > settings > defaults; special-case faux/scripted model via config.ResolveModel
- **Print mode** (`internal/modes/print.go`): subscribe to agent events, persist per-event to session storage, fan-out to EventSink (text or JSONL), queue follow-ups, emit exit codes (0/1/2/130)
- **EventSink abstraction** (`internal/modes/sink.go`, `sink_json.go`): TextSink (final assistant text to stdout, errors to stderr), JSONSink (flattened JSONL per event)
- **TUI mode** (`internal/tui/`, `cmd/mixi/run_tui.go`): Bubble Tea application running on tty (auto-default when stdin is terminal); event bridge pumps agent.Subscribe() into tea model; approval modal answers permission engine via perm.PendingAsk.Reply channel
- **TUI components** (10 sub-packages): root model + key handling, transcript viewport with sticky-bottom, message/tool views (streaming states, glamour markdown, diff rendering), approval modal overlay, input editor (history ring ×50, slash autocomplete, Ctrl+G external editor), status bar (live model/think/ctx%/$cost from obs.Tracker/mode/jobs), keymap table (27 bindings), slash command registry (includes `/cost` with per-model breakdown + `/mcp` status)
- **TUI asker** (`internal/perm/ask.go` + `internal/tui/` adapter): replaces HeadlessAsker; PendingAsk channels approval requests to modal; NotifyAsker is the channel-driven asker interface (reused by RPC phase 14)
- **Signal handling** (`cmd/mixi/main.go`): SIGINT → graceful abort (exit 130 in print, Esc interrupt in TUI), 2nd SIGINT/SIGTERM → cleanup + exit
- **Session wiring** (`cmd/mixi/setup.go`): openSession per flags (fresh / -c / --resume / --fork / --no-save); historyFromSession seeds agent.History from active path
- **TUI logging** (`cmd/mixi/run_tui.go`): slog redirected to <sessiondir>/mixi.log; never stdout/stderr during interactive mode
- **MCP setup** (`cmd/mixi`): startMCP initializes MCP manager from config, bounded startup wait, wiring MCP tools after registry built, honors --no-mcp flag, exposes /mcp status in TUI

### Layer 1: Core Types (`internal/ai`)
- **Sealed unions** (marker interfaces + JSON discriminators): `Content`, `Message`, `StreamEvent`
- **Provider abstraction**: `Provider` interface (register at init, stream events, close on Done/Error)
- **Models:** global registry of known model IDs + defaults (claude-3.5-sonnet, gpt-4, etc.)
- **No imports of other internal packages** — type system is self-contained

### Layer 2: Permission Engine (`internal/perm`)
- **Decision pipeline:** explicit rules (--deny flags > --allow flags > settings deny > settings allow) → baseline screens (denyPatterns regex for bash, write outside cwd subtree, secret-glob forced-ask for read/grep) → session grants → mode defaults → headless asker
- **Modes:** plan (read-only: read allowed, write/execute denied), prompt (default: read allowed, rest asks), auto-edit (read+write allowed, execute asks), yolo (all allowed except explicit denies)
- **Rules:** `bash(prefix*)` command glob, `read/write/edit(glob)` path doublestar glob (relative anchored at cwd; `**` floats), `mcp__server__tool` exact/glob; doublestar validated at parse time
- **Baseline screens (non-yolo):** denyPatterns regex hard-deny for bash commands; write/edit operations outside cwd subtree (realpath-resolved) trigger forced-ask; read/grep of secret paths (`**/.env*`, `**/*_rsa`, `**/credentials*`) trigger forced-ask
- **Session grants:** in-memory per session, generalized from CallInfo (bash: first two tokens + `*`; file ops: directory + `/**`); `/permissions` listing for UI (later phase)
- **HeadlessAsker:** deny with actionable reason unless mode is yolo or auto-edit; denial is an LLM-visible IsError tool result
- **SandboxSpec:** unused v1 seam; allows bash execution to adopt landlock/seccomp without interface change

### Layer 2b: Provider Implementation (`internal/ai/anthropic`, `internal/ai/faux`)
- **Anthropic:** Converts Anthropic wire format → normalized `StreamEvent` order contract; handles extended thinking (redacted_thinking), block tracking, content indexing; SSE decoder + partial JSON repair for streaming text/tool calls; caches model metadata; retry hooks apply to API calls
- **Faux (E2E test provider):** Scripted, always-compiled model `faux/scripted` replays turns from JSON file (MIXI_FAUX_SCRIPT env); per-process sync.Once load; delegates playback to agenttest.Provider for identical wire behavior to unit tests; used in cmd/mixi E2E suite for SIGINT testing and subprocess re-exec patterns

### Layer 2c: MCP Client & Wire Transport (`internal/mcp`, `internal/wire`)
- **Wire package** (`internal/wire/jsonl.go`): JSONL framing layer for reliable subprocess communication; atomic writes, 1MiB per-line cap, optional write deadline for stuck servers
- **MCP protocol** (`internal/mcp/protocol.go`): structs for MCP 2025-06-18 and 2025-03-26 fallback; initialization, tools request, call/result/error message types
- **MCP Transport** (`internal/mcp/transport.go`): interface abstraction for protocol I/O; stdio impl (`internal/mcp/stdio.go`) spawns subprocess with Setpgid (Unix), redirects stderr to DEBUG logs, escalates shutdown SIGTERM→2s→SIGKILL
- **MCP Client** (`internal/mcp/client.go`, `client_calls.go`): id-tracked routing, per-call default 30s timeout, malformed-line cap (10/min), handles RPC errors and crashes mid-call
- **MCP Manager & Supervisor** (`internal/mcp/manager.go`, `supervisor.go`): lifecycle state machine (CONFIGURED→INITIALIZING→READY⇄RESTARTING→FAILED/CLOSED), exponential backoff (1s/2s/4s), 3-strike disable, /mcp reconnect command, tool registration after startup
- **Server config** (`internal/mcp/server_config.go`): decode from settings.json mcpServers block, ${VAR} env expansion, timeoutMs per-server override
- **Tool adapter** (`internal/mcp/tooladapter.go`): wraps MCP tools as internal/tools.Tool; names as `mcp__<server>__<tool>`, description prefix `[mcp:<server>] `, schema passthrough with permissive fallback (uncompilable schemas don't crash)

### Layer 2d: Extension Host (`internal/ext`)
- **Extension host** (`internal/ext/host.go`): discovery from settings block + ~/.mixi/extensions/ + .mixi/extensions/, per-extension lifecycle, event fan-out (FIFO queue cap 256, drop-oldest+WARN), blocking tool_call gate (sequential ordering, 5s per response, 3-strike→Disabled)
- **Extension lifecycle** (`internal/ext/extension.go`, `internal/ext/supervisor.go`): startup (spawn → hello handshake 5s timeout), state machine (Starting/Ready/Degraded/Crashed/Disabled), restart backoff (1s/2s/4s), 3-strike disable, in-flight call recovery (fail-open on crash)
- **Protocol** (`internal/ext/protocol.go`): version-1 experimental mark (sign-off phase 16); hello/ready/message types; event subscription (ready, session_start, agent_start/message_update/message_end/tool_start/tool_end, agent_end, session_shutdown, notice)
- **Action API** (`internal/ext/actions.go`, `internal/ext/api.go`): send_message (steer/followUp/nextTurn), append_entry (session), set_status (TUI status bar), notify (notice + stderr in headless), ask_select (headless→first option+WARN), register_command (hello-only)
- **Tool bridge** (`internal/ext/toolbridge.go`, `internal/ext/adopt.go`): tool_call blocking gate (sequential by registration order, 5s timeout, rejects on ≥3 strikes), tool result round-trip via wire protocol, permission engine integration (ext tools match `ext__*` pattern)
- **Process management** (`internal/ext/proc.go`, `internal/ext/gate.go`): Setpgid spawn (Unix), stderr merge, SIGKILL on shutdown (3s grace), malformed-line detector (≥10/min→Disabled), wire cap violation (1MiB)
- **Configuration** (`internal/ext/config.go`): decode from settings.json extensions block, ${VAR} expansion, discovery order (configured first, then filesystem)

### Layer 3: Agent Runtime (`internal/agent`)
- **ToolCallFilter interface:** sits ahead of BeforeToolCall hook; permission engine + extension host implement this for filter-first pipeline; filters fail closed (error on filter crash → tool denied). Extension host filters here before gate deadlines apply.
- **Two-level loop structure:**
  1. **Outer:** follow-up extend a run; loop restarts when follow-ups enqueued
  2. **Inner:** stream assistant turn → extract tool calls → filter tool calls (ToolCallFilter chain) → dispatch tools → collect results → advance turn counter
- **Message types:**
  - `ModelMessage` (ai.Message) — provider-level user/assistant/toolResult
  - `BashExecution`, `CompactionSummary`, `BranchSummary` — harness items
  - `AgentMessage` interface (sealed union) — all of above
- **Event contract** (per-run order, documented in `events.go`):
  ```
  EvAgentStart → [per turn: EvTurnStart → EvMessageStart → EvMessageUpdate* →
  EvMessageEnd → EvToolStart/End* → EvTurnEnd] → EvAgentEnd
  EvRetryStart/End may precede a turn's EvMessageStart
  ```
- **Queues:** steering (inject mid-turn), follow-up (extend run after completion), bounded capacity (ErrQueueFull)
- **Retry:** transient errors (network) retry with jitter; fatal errors (model not found) end run immediately
- **Hooks:** `TransformContext`, `GetAPIKey`, `BeforeToolCall`, `AfterToolCall`, `ShouldStop` — applied per turn/call
- **Tool execution:** parallel by default, sequential if any tool declares `ExecSequential` or config forces it
- **Panic recovery:** tool panics → error result, run continues
- **Structured logging** (phase 13): INFO records for turn lifecycle (turn, model, stop_reason, tool_calls, latency_ms) and tool execution (tool, call_id, is_error, latency_ms); logged via obs package to daily JSONL + stderr mirror per config

### Layer 4: Built-in Tools (`internal/tools`)

**Core Interface & Registry**
- `Tool` interface: `Name()`, `Description()`, `Schema()`, `Mode()`, `Execute()`
- `Registry`: name-keyed, build-once, read-safe; `RegisterBuiltins()` returns `*JobTable` for agent shutdown
- `ToolResult`: content blocks (text/image) + metadata (Details, IsError)
- `ToolUpdate`: streaming updates from long-running tools (non-blocking via select+default)

**Nine Built-in Tools**
| Tool | Mode | Behavior |
|------|------|----------|
| `read` | Parallel | Files: 1-indexed offset/limit, head-truncate (max 2000 lines, 50 KiB), line-numbered; images: sniff PNG/JPEG/GIF/WebP, downscale >2000px via stdlib draw |
| `write` | Parallel | Atomic write: mkdir-p parent, temp file + rename; per-realpath mutex (mutqueue), optional fsync |
| `edit` | Parallel | Fuzzy-match file (NFKC normalize, trailing-ws strip, smart quotes/dashes/spaces), apply to whole file, preserve CRLF/BOM; closest-line hint on no-match |
| `bash` | Sequential | Exec `$SHELL -c` with Setpgid (Unix process group; Windows stub); interleaved stdout+stderr, rolling-tail accumulator (spill >100 KiB to temp file); 100ms throttled updates; timeout SIGTERM→2s grace→SIGKILL group |
| `bash_output` | Parallel | Read cursor position from background job; no-op if job finished |
| `kill_bash` | Parallel | Terminate background job (SIGTERM→2s→SIGKILL group) |
| `grep` | Parallel | Ripgrep (rg) required on PATH; JSON-lines mode, clip lines to 500 bytes, max 2000 results; install-hint error if rg absent |
| `find` | Parallel | Prefers fd (respects .gitignore); fallback: pure-Go WalkDir + doublestar glob, no .gitignore (note: "[fd not found: .gitignore not respected]"), max 2000 results |
| `ls` | Parallel | Pure Go: os.ReadDir, limit 2000 entries |

**Shared Infrastructure**
- **truncate.go**: Head-truncate (keep lines until max-lines or max-bytes), tail-truncate (last N lines), UTF-8-safe boundary handling
- **accumulator.go**: Rolling-tail buffer (2×MaxBytes in memory); lazy spill to temp file; cursor reads for bash output streaming
- **job_table.go**: Background job registry (b1, b2, ...) for bash background=true; KillAll for agent shutdown
- **mutqueue.go**: Per-realpath mutex pool with refcount cleanup; serializes edits/writes to same file, parallels different files
- **bintools.go**: lookPath wrapper; per-OS rg/fd install hints (install-hint error vs auto-download)
- **procgroup_unix.go/procgroup_windows.go**: Unix Setpgid group kill (SIGTERM→2s→SIGKILL); Windows best-effort PID kill
- **read_image.go**: Magic-byte sniff (PNG, JPEG, GIF, WebP); downscale >2000² via x/image/draw
- **editmatch.go**: NFKC + smart-quote/dash/space normalization table; exact→fuzzy match; Sørensen–Dice distance for closest-line hint
- **edit_details.go**: UI metadata (diff, patch, firstChangedLine); never sent to LLM

**Constants (Pi-compatible)**
- `MaxLines = 2000` — max lines per tool result
- `MaxBytes = 50 KiB` — max bytes per result
- `GrepMaxLineLen = 500` — clip individual grep lines
- `BashUpdateThrottle = 100ms` — min interval between streamed updates
- `BashDefaultTimeout = 120s`, `BashMaxTimeout = 600s`

**Key Semantics**
- **Edit fuzzy-match:** Normalizes entire file content; if any edit needs fuzzy (not exact), rewrites whole file to normalized form
- **Bash sequential:** Even if no tool declares ExecSequential, bash forces sequential (one-at-a-time) to guarantee output ordering
- **Process-group kill:** Unix only (syscall.SysProcAttr{Setpgid}); Windows: best-effort PID kill (no true group termination)
- **Grep required:** rg must be on PATH; no fallback, no auto-download (install-hint error)
- **Find fallback:** fd absent → pure-Go WalkDir+doublestar, output note warns .gitignore not respected

### Layer 5: Schema Validation (`internal/schema`)
- **Compile:** JSON Schema → validator, rejects unknown keywords
- **Coerce:** type promotion (string→number, null→empty string), unsets bad fields
- **ErrFmt:** LLM-readable validation errors (no internal stack traces)

### Layer 6: Session Persistence (`internal/session`)
- **JSONL tree storage:** Header (v1) + discriminated entry types (11 total: message[pinned], model_change, thinking_level_change, active_tools_change, compaction, branch_summary, custom, custom_message, label, session_info, leaf)
- **Entry codec:** type-discriminated JSON with message payloads delegated to ai.MarshalMessage/UnmarshalMessage
- **Storage interface:** Header/Append/Get/PathToRoot/LeafID/SetLeaf/Entries/Close with two impls (jsonlStore on disk, memStore for tests/--no-save)
- **Deferred first write:** no file/lock until first message entry; empty sessions leave zero disk artifacts
- **File locking:** flock(LOCK_EX|LOCK_NB) on Unix + LockFileEx on Windows (x/sys); optional fsync per entry; pid-hint sidecar
- **Crash recovery:** stream-scan loader with 10 MiB line cap; warns + truncates partial trailing line; never truncates without valid header; rejects version>1 with upgrade hint
- **Tree operations:** byId index + leaf replay; PathToRoot, SetLeaf, CommonAncestor, fork (file-order prefix copy + parentSession header)
- **Manager:** ~/.mixi/sessions/<cwd-slug>/<ts>_<uuidv7>.jsonl; Create/Open/ContinueRecent(newest mtime)/Fork/InMemory
- **Entry IDs:** last-8-hex of uuidv7 (deliberate timestamp-prefix deviation to avoid collisions); ≤100 retries; full-uuid fallback

### Layer 7: Compaction & Working Set (`internal/compact`, `internal/workset`)

**Compaction (`internal/compact`):**
- **Token estimation:** usage-anchored (provider-reported tokens); falls back to chars/4 post-compaction until next provider call
- **Serialization:** Pi-compatible turn serialization (messages + tool results); custom-entry file-list merge
- **Cut-point selection:** never breaks at toolResult, splits on turn boundary (repeat window from previous firstKeptEntryId), merges split-turn second summary into first via `---` separator
- **LLMSummarizer:** generation prompts per turn type (user/assistant/tool); conversation context for summarization
- **Compactor:** state machine enforcing single-entry-per-success; guards against duplicate compaction
- **Controller:** bridges agent loop ↔ session; synchronous persistence hook (OnMessage), 3 triggers (pre-flight once/turn, post-turn, overflow retry-once), pinned-entry survival mapping, workset persistence via custom{workingset} entry

**Working Set (`internal/workset`):**
- **FileStamp:** mtime+size fast-path, sha256 confirmation for unchanged files
- **File-freshness tracking:** detects disk changes; "[system] Files changed on disk…" notice capped at 20 files
- **ContextBuilder:** assembly pipeline (summary → pinned → kept history → staleness notice); budget-aware
- **Budget pipeline:** trim old >1 KiB tool results (largest-first, outside last 2 turns) → compact once → hard error if overflow persists
- **Custom-entry persistence:** WorkingSet saved as custom{workingset} in session for multi-turn freshness tracking
- **Projection-only:** context assembly never mutates underlying storage

## Message Flow

1. **User calls `Agent.Prompt(context, message)`**
   - Message queued to steering, run started if idle
2. **OnMessage hook (sync persistence)** → append to session, emit harness events (Phase 8)
3. **runLoop pulls steering queue** → emit EvMessageStart/End → inject into history
4. **Stream LLM** → receive AssistantMessage, emit EvMessageUpdate* (render-only)
5. **Extract tool calls** from message
6. **executeToolCalls:**
   - Parallel: start all calls, collect as each finishes (order: completion order in race)
   - Sequential: start one, collect, repeat (order: source order)
   - FileObserver records reads/writes/edits into working set (Phase 8)
7. **Emit EvToolStart/End** per call, emit EvToolUpdate* if tool streams
8. **Emit EvTurnEnd** with full message + results
9. **AfterTurn hook (post-turn compaction trigger)** → if context > keepRecentTokens, attempt compact once (Phase 8)
10. **Check steering queue:** if new messages, loop back to step 3
11. **OnContextOverflow hook (overflow recovery)** → if context budget exceeded, compact + retry once (Phase 8)
12. **Check follow-up queue:** if messages and no tool calls were made, continue one more turn
13. **Emit EvAgentEnd** with all new messages + end reason (Done, MaxTurns, Error, Aborted)

## Test Coverage

- **50+ test files** across all packages
- **Passing tests:** 279 baseline + 15 Phase 8 + 11 Phase 9 (perm 90.9% coverage) = 305 total
- **Test scope:** CLI E2E: 6 tests incl. SIGINT abort + signal handling; config: flags + merging + resolution; modes: print/JSON/follow-ups/stats; faux: scripted provider; agent/loop/toolexec/retry/hooks, AI types/events/registry/stream, schema validation/coercion, nine built-in tools, session storage/tree/lock/loader/manager, compaction/working-set, permission engine (policy/engine/preview)
- **6 env-skips:** rg/fd absent on test machine (error paths covered via fake lookPath)
- **Coverage:** all 15 packages pass `-race -count=1`; CLI E2E zero-flakes across 145+ test executions
- **Race detector:** green (no data races detected)
- **Goleak:** green (no goroutine leaks)

## Key Files

### CLI & Configuration (600+ LOC)
- `cmd/mixi/main.go` (CLI entry, realMain, signal handling, mode dispatch)
- `cmd/mixi/setup.go` (openSession, historyFromSession, systemPrompt, streamOpts resolution)
- `cmd/mixi/permissions.go` (buildPermissionEngine, permissionFilter adapter on ToolCallFilter interface)
- `cmd/mixi/e2e_test.go` (subprocess re-exec E2E pattern)
- `cmd/mixi/e2e_runner_test.go` (E2E runner with signal injection)
- `cmd/mixi/e2e_permissions_test.go` (deny rule, yolo mode, secret-glob forced-ask scenarios)
- `internal/config/config.go` (Settings struct + PermissionSettings, JSON load+merge, ${ENV} expansion, deny lists append-only)
- `internal/config/flags.go` (stdlib flag, repeatable flags, WasSet tracking, --permission-mode and --allow/--deny)
- `internal/config/runtime.go` (Resolve precedence, ResolveModel incl. faux/scripted special case)
- `internal/modes/sink.go` (EventSink interface, TextSink: assistant text → stdout, errors → stderr)
- `internal/modes/sink_json.go` (JSONSink: flattened JSONL event records)
- `internal/modes/print.go` (RunPrint: subscribe, persist-per-event, sink fan-out, follow-up queueing, usage stats, exit codes)
- `internal/ai/faux/faux.go` (scripted provider, sync.Once load, script playback)
- 9 E2E tests: SIGINT abort (exit 130), 2nd SIGINT cleanup, tool call write+persist, JSON JSONL output, follow-ups+stats, stdin pipe implies print mode, usage error exit codes, permission deny rules, secret-glob scenarios

### Observability & Replay (600+ LOC)
- `internal/obs/log.go` (Setup: slog JSON formatting → ~/.mixi/logs/mixi-<YYYY-MM-DD>.jsonl, daily rotation keep 7, stderr mirror level control, --log-level + MIXI_LOG env resolution, TUI-specific no-mirror behavior)
- `internal/obs/usage.go` (Tracker: per-turn {turn, model, usage, durMs, toolCalls}, session totals + byModel breakdown, in-flight pending snapshot; UsageSink interface for EventSink + TUI statusbar integration)
- `internal/obs/replay.go` (RunReplay: JSONL entry load, entry→event synthesis, read-only session open, no API calls)
- `internal/obs/replay_render.go` (Replay rendering: plain-text transcript, assistant messages + tool execution simulation, thinking block rendering, pacer with recorded-timestamp gaps capped 2s)
- `cmd/mixi/replay.go` (runReplay: subcommand entry point, ReplayOptions wiring)
- `internal/config/flags.go` (--speed 1x|5x|instant, --until <entryId>, --verbose, --log-level; enum validation)
- `cmd/mixi/main.go` (log setup first in realMain, session_id field after store open, replay dispatch before session/API-key checks)
- 4 test files: schema validation, level precedence, degrade-on-unwritable-dir, tracker folding, replay byte-exact golden, --until truncation, locked-session replay, pacer

### Built-in Tools (1,500+ LOC)
- `truncate.go` (head/tail truncation, UTF-8 boundaries)
- `accumulator.go` (rolling-tail buffer, temp-file spill, cursor reads)
- `job_table.go` (background job registry, shutdown KillAll)
- `mutqueue.go` (per-realpath mutex pool, refcount cleanup)
- `bintools.go` (lookPath wrapper, install-hint errors)
- `procgroup_unix.go` / `procgroup_windows.go` (process-group kill strategies)
- `read.go` / `read_image.go` (file/image read, sniff, downscale)
- `write.go` (atomic write, temp+rename, fsync)
- `edit.go` / `editmatch.go` / `edit_details.go` (fuzzy match, normalization table, diff/patch metadata)
- `bash.go` / `bash_bg.go` (shell exec, interleaved output, job lifecycle, timeout)
- `grep.go` (ripgrep wrapper, JSON-lines parse)
- `find.go` (fd preferred, pure-Go WalkDir fallback, doublestar glob)
- `ls.go` (pure-Go directory listing)
- `builtins.go` (RegisterBuiltins entry point)
- 13 test files: 65 passing, 6 env-skips (rg/fd absent), 81.9% coverage, race green

### Session Storage (2,250+ LOC)
- `entry.go` (Header, Entry interface, 11 entry types)
- `entry_json.go` (wire struct marshaling, discriminator routing)
- `storage.go` (Storage interface definition)
- `store.go` (jsonlStore: disk JSONL, deferred write, optional fsync)
- `memstore.go` (in-memory store for tests)
- `loader.go` (stream-scan, 10 MiB line cap, crash recovery, version check)
- `lock.go` / `lock_windows.go` (flock/LockFileEx, pid-hint sidecar)
- `manager.go` (Manager: session lifecycle, ~mixi/sessions/{cwd-slug}/{ts}_{id}.jsonl)
- `tree.go` (byId index, PathToRoot, SetLeaf, CommonAncestor, fork)
- `ids.go` (8-hex entry IDs from uuidv7, collision retry strategy)
- 8 test files: 38 passing, -race -count=5 stable, 86.6% coverage

### Agent Runtime (3,100+ LOC)
- `agent.go` (Config, Agent type, New, Prompt/Continue/Subscribe methods)
- `loop.go` (runLoop, inner/outer loop logic, stream handling, ToolCallFilter chain)
- `toolexec.go` (executeToolCalls, filter pipeline, parallel/sequential dispatch, panic recovery)
- `hooks.go` (Hooks interface, ToolCallFilter interface, OnMessage/AfterTurn/OnContextOverflow optional hooks, Notify for harness events)
- `events.go` (sealed Event union, event order contract)
- `messages.go` (AgentMessage union, ModelMessage, BashExecution, CompactionSummary)
- `queue.go` (boundedQueue, DrainAll/DrainOne modes)
- `retry.go` (classifyError, retryDelay, jitter + retry-after)
- `sysprompt.go` (BuildSystemPrompt, tool definitions, context file embedding)
- `main_test.go` (test helpers, scripted stream runner)

### Permission Engine (550+ LOC)
- `policy.go` (Mode enum, Category mapping, Rule struct + parse, Policy assembly, rule-matching logic)
- `engine.go` (Engine: Decide decision pipeline, mode defaults table, ask escalation)
- `ask.go` (AskRequest, AskDecision enum, Asker interface, HeadlessAsker impl)
- `callinfo.go` (CallInfo: tool category/command/path extraction from ToolCall)
- `preview.go` (Preview generation: edit dry-run diffs via Myers diff, write/bash/mcp summary)
- `diff.go` (Line-based Myers diff, unified format emitter)
- `globmatch.go` (Bash command wildcard match, path doublestar match via bmatcuk package)
- `grants.go` (Grant store: in-memory session grants, generalization rules)
- 3 test files: 12 tests covering decision matrix, rule parsing, secret-glob detection, 90.9% coverage

### Compaction (750+ LOC)
- `estimator.go` (Estimator interface, UsageAnchoredEstimator, FallbackEstimator for post-compaction staleness)
- `serializer.go` (Pi-compatible turn serialization, message+result encoding)
- `cutter.go` (cut-point walk, never-at-toolResult rule, turn-boundary split, repeat-window resume)
- `prompts.go` (4 ported summarization prompts for user/assistant/tool/mixed turns)
- `summarizer.go` (LLMSummarizer integration, conversation context assembly)
- `compactor.go` (Compactor state machine, single-entry-per-success guard, Controller bridge)
- `controller.go` (session ↔ agent loop sync, 3 compaction triggers, pinned mapping, workset persistence)
- 8+ tests: estimator anchor/fallback, cut-point logic, summary prompts, controller triggers

### Working Set (450+ LOC)
- `filestamp.go` (FileStamp mtime+size, sha256 confirm, stale detection)
- `working_set.go` (WorkingSet custom-entry codec, file-list merge)
- `context_builder.go` (ContextBuilder: summary→pinned→kept→staleness, trim→compact→error budget pipeline, projection-only)
- 7+ tests: FileStamp staleness, budget pipeline, context assembly

### Wire Transport (200+ LOC)
- `internal/wire/jsonl.go` (JSONL framing, line cap, atomic writes, write deadline)
- Tests: round-trip encode/decode, oversize line rejection, malformed input handling

### MCP Client (2,000+ LOC)
- `internal/mcp/protocol.go` (MCP message types, version negotiation)
- `internal/mcp/transport.go` (Transport interface, protocol I/O abstraction)
- `internal/mcp/stdio.go` (subprocess spawn, Setpgid, stderr→DEBUG, SIGTERM escalation)
- `internal/mcp/client.go` (request routing, response dispatch, error handling)
- `internal/mcp/client_calls.go` (per-call execution, timeout, malformed cap)
- `internal/mcp/manager.go` (MCP lifecycle, tool registration, server state)
- `internal/mcp/supervisor.go` (lifecycle state machine, backoff, reconnect logic)
- `internal/mcp/server_config.go` (settings.json decode, env expansion)
- `internal/mcp/tooladapter.go` (adapter to internal/tools.Tool interface)
- Tests: chaos suite vs fake compiled server, everything-server conformance (13 tools live)

### Extension Host (1,000+ LOC)
- `internal/ext/protocol.go` (version:1 experimental, hello/ready/message types, event subscription)
- `internal/ext/host.go` (discovery loop, spawn/handshake, event fan-out FIFO, blocking gate pipeline)
- `internal/ext/extension.go` (per-ext state machine, queue, strike counter)
- `internal/ext/supervisor.go` (lifecycle CONFIGURED→INITIALIZING→READY⇄RESTARTING→FAILED/CLOSED, backoff 1s/2s/4s, 3-strike disable)
- `internal/ext/actions.go` (send_message, append_entry, set_status, notify, ask_select, register_command)
- `internal/ext/toolbridge.go` (blocking gate sequential ordering, 5s response timeout, ≥3-strike demote)
- `internal/ext/config.go` (settings.json extensions block decode, ${VAR} expansion)
- `internal/ext/proc.go` (Setpgid spawn, stderr merge, graceful shutdown SIGKILL)
- `internal/ext/gate.go` (gate state machine, timeout strike logic)
- `cmd/mixi/ext_setup.go` (discovery + wiring before agent build)
- `cmd/mixi/e2e_extensions_test.go` (permission_gate example end-to-end, --no-extensions flag verification)
- `examples/extensions/permission_gate/main.go` (Go sample: blocks rm -rf via tool_call)
- `examples/extensions/status_line/status_line.sh` (Shell sample: sets footer status via set_status)
- Tests: 6 test files, conformance suite (handshake, gate block, mutation chain, timeout, crash/restart/disable), wire cap violation, malformed-msg rate cap

### AI Layer (500+ LOC)
- `types.go` (Content/Message/StreamEvent sealed unions, Model, Context)
- `json.go` (JSON marshal/unmarshal with discriminators)
- `events.go` (StreamEvent order contract)
- `registry.go` (Provider registry, Resolve, Stream)
- `models.go` (Known models, LookupModel)
- `stream.go` (Stream func, error stream fallback)

### Anthropic Provider (500+ LOC)
- `provider.go` (Provider impl, Anthropic API call)
- `request.go` (RequestMessage builder, tool definitions)
- `convert.go` (Anthropic wire → normalized StreamEvent)
- `events.go` (Anthropic event types: text_start, thinking_delta, tool_use_start, etc.)
- `block_tracker.go` (Content block index tracking during streaming)

### Schema Validation (300+ LOC)
- `schema.go` (Compile: JSON Schema → validator)
- `coerce.go` (Type promotion, field unsetting)
- `errfmt.go` (LLM-readable error formatting)

## Configuration

**Agent Config** (agent.go):
```go
Model        ai.Model              // Required: model ID + provider
Tools        *tools.Registry       // Default: empty registry
Hooks        Hooks                 // Default: no-op
Filters      []ToolCallFilter      // Default: nil (permission engine can install)
SystemPrompt string                // Default: empty (BuildSystemPrompt fills)
StreamOpts   ai.StreamOptions      // Default: empty (provider decides)
MaxTurns     int                   // Default: 80 (0 = unlimited, <0 = 80)
SequentialTools bool               // Default: false (parallel)
SteerDrain   DrainMode             // Default: DrainAll
Stream       StreamFunc            // Default: registryStream (provider lookup)
Log          *slog.Logger          // Default: slog.Default()
History      []AgentMessage        // Default: nil (session resume seeding, Phase 7)
```

**Agent Contract Additions** (Phase 10):
- `Agent.SetModel(ai.Model)` — change model mid-session (TUI Ctrl+P); affects next run
- `Agent.Model() ai.Model` — query current model
- `Agent.SetThinking(level string)` — set extended thinking level (off/low/medium/high); affects next run
- `Agent.Thinking() string` — query current thinking level
- `Agent.Running() bool` — check if agent is actively streaming/executing tools

**Phase 11 Enhancements:**
- `tools.Registry` now guarded by RWMutex for concurrent tool registration (MCP tools register after startup)
- `Config.Tools` injection point for MCP tools; manager discovers tools post-init and calls Registry.Register
- `runTUI` now accepts optional MCPFleet manager for /mcp command support

**Permission Settings** (internal/config via ~/.mixi/settings.json):
```
permissions.mode                   // plan | prompt | auto-edit | yolo; default: prompt
permissions.allow[]                // Allow rules (bash(prefix*), read/write/edit(glob), mcp__*)
permissions.deny[]                 // Deny rules (same syntax; deny appends across layers)
permissions.denyPatterns[]          // Bash command regex blacklist; default: none
```

**Asker Interface** (internal/perm):
- `HeadlessAsker` — print mode, denies with actionable reason; legacy phase 9 implementation
- `NotifyAsker` (phase 10+) — channel-driven asker for TUI + future RPC modes; implements `Asker` interface with PendingAsk struct (buffered Reply channel, exactly-once delivery)

**Compaction Settings** (internal/config via ~/.mixi/settings.json):
```
compaction.disabled              // Kill-switch: bool (default: false)
compaction.reserveTokens         // Minimum context budget kept free; default: 16384
compaction.keepRecentTokens      // Soft limit on working set; default: 20000
```

**MCP Settings** (internal/config via ~/.mixi/settings.json):
```
mcpServers: {
  "server-name": {
    "command": "binary-name",           // required: executable name
    "args": ["arg1", "arg2"],           // optional: args to pass
    "env": {"VAR": "${VAR_NAME}"},      // optional: ${VAR} expansion for env vars
    "timeoutMs": 30000                  // optional: per-call timeout (ms); default 30000
  }
}
```

**CLI flag:**
- `--no-mcp` — skip MCP manager initialization, disable all MCP tool registration

**Extension Settings** (internal/config via ~/.mixi/settings.json):
```
extensions: {
  "ext-name": {
    "command": "binary-name",           // required: executable path
    "args": ["arg1", "arg2"],           // optional: args to pass
    "env": {"VAR": "${VAR_NAME}"}       // optional: ${VAR} expansion for env vars
  }
}
```
Discovery order: settings block first (configured), then ~/.mixi/extensions/, then .mixi/extensions/ (project-local).

**CLI flag:**
- `--no-extensions` — skip extension host initialization, disable all extension discovery and loading

**Tool Observer** (tools.Options):
```go
Observer     FileObserver          // Optional: tracks read/write/edit operations into working set
```

## Concurrency Model

- **Agent methods:** Prompt/Continue/Subscribe/Abort guarded by single mutex
- **Event bus:** queues subscribers per channel capacity (16 default), drops render events on overflow
- **Tool execution:** parallel goroutines with atomic counters + WaitGroup (no shared tool state assumed)
- **Queues:** mutex-guarded FIFO (steering/follow-up)
- **No RwMutex:** provider registry uses RwMutex, agent loop holds no state between calls

## Integration Points

1. **Provider lookup:** `ai.Resolve(model.API)` → error if not registered
2. **Tool dispatch:** `tools.Registry.Get(callName)` → nil if not found (error result)
3. **Permission filter chain:** `Config.Filters` run in order before BeforeToolCall hook; denial is `BlockDecision{Reason: "..."}` sent to model as IsError tool result
4. **Schema validation:** `schema.Compile(tool.Schema())` → LLM-readable errors on bad args
5. **Hooks:** called at transform context / get API key / before/after tool call / should stop checkpoints
6. **Logging:** structured logs to Config.Log

## Dependencies

**Direct:**
- `github.com/bmatcuk/doublestar/v4` — glob matching for find.go WalkDir fallback (Phase 5), permission engine path globs (Phase 9)
- `golang.org/x/image` (draw, webp) — image downscale, WebP decode for read_image.go (Phase 5)
- `github.com/google/uuid` — uuidv7 generation for session IDs (Phase 6)
- `golang.org/x/sys` — Windows LockFileEx (Phase 6, untested best-effort)
- `github.com/charmbracelet/bubbletea` — TUI framework (Phase 10)
- `github.com/charmbracelet/bubbles` — TUI widgets: viewport, textarea, spinner (Phase 10)
- `github.com/charmbracelet/lipgloss` — TUI styling, layout (Phase 10)
- `github.com/charmbracelet/glamour` — markdown renderer for transcript (Phase 10)
- `golang.org/x/exp/teatest` — TUI E2E testing helpers (test-only, Phase 10)

**Existing:**
- Standard library: context, encoding/json, io, os, syscall, time, crypto/sha256, etc.
- (Phase 11 note: no new external deps added; wire and mcp use only stdlib + existing internal packages)

## Stability Notes

- **Backward compatibility:** all wire types (ai.Message) are versioned and can evolve via new discriminator values
- **Extension points:** Tool interface is stable; new tool types registered at startup
- **Provider evolution:** new providers register themselves; existing logic unaffected
- **Schema validation:** coercion is conservative (promote types safely, omit bad fields rather than fail)
- **Built-in tools:** semantics frozen in tool descriptions (edit whole-file rewrite on fuzzy, bash sequential, grep requires rg, find falls back to pure-Go)
