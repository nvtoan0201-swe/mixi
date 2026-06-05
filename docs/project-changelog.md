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

## Phase 7: CLI & Print Mode E2E (2026-06-04)

### Four New Packages

#### `cmd/mixi` (CLI Entry Point)
- Implement realMain: flag parsing, config resolution, session open, tool wiring, mode dispatch
- Settings precedence: flags > --config > .mixi/settings.json > ~/.mixi/settings.json > defaults
- Session open per flags: fresh (default), -c/--continue (most recent), --resume/-fork (specific id/path), --no-save (in-memory)
- Tool registry wiring + signal handling (SIGINT → graceful abort exit 130; 2nd SIGINT/SIGTERM → cleanup)
- Mode dispatch: print mode live; TUI/RPC/replay stubbed "not yet available"
- API-key-from-env hooks (ANTHROPIC_API_KEY, OPENAI_API_KEY)

#### `internal/config` (Settings & Flags)
- Settings struct: model, permissions, compaction, files, mcpServers, extensions
- JSON load+merge across layers (global, project, extra); deny lists append-only
- ${ENV} brace-only expansion (e.g., ${ANTHROPIC_API_KEY})
- Flag parsing: stdlib flag, repeatable flags (--message, --allow, --deny), WasSet tracking
- RuntimeConfig.Resolve: precedence logic (flags > settings > defaults); ResolveModel handles faux/scripted special case

#### `internal/modes` (Headless Run Modes)
- EventSink interface: abstract emission point for all agents events
- TextSink: final assistant text → stdout, errors → stderr
- JSONSink: flattened JSONL event records (one line per event)
- RunPrint: subscribe to agent, persist-per-event to session storage, fan-out to sink, queue follow-ups, print usage stats on request, exit codes (0/1/2/130)

#### `internal/ai/faux` (E2E Test Provider)
- Scripted, always-compiled provider (model faux/scripted)
- JSON script via MIXI_FAUX_SCRIPT env; sync.Once per-process load
- Delegates playback to agenttest.Provider for identical wire behavior to unit tests
- Enables E2E testing of CLI without network access or flaky provider calls

### Test Results & Quality
- 6 E2E tests in cmd/mixi (subprocess re-exec pattern): SIGINT abort (exit 130), 2nd SIGINT cleanup, tool call write+persist, JSON output JSONL, follow-ups+stats, stdin pipe implies print mode, usage error exit codes (exit 2)
- 30 tests across config/modes/faux packages
- **279 total tests** project-wide
- **145 test executions** in E2E suite; zero flakes, race-clean
- No critical review issues; M1 slow-consumer edge deferred to phase 16

### Key Deliverables
- First end-to-end binary: `mixi -p "prompt"` with live Anthropic calls
- Print mode text output (final assistant text) and --output json (JSONL event stream)
- --session-dir, --resume/-c/--fork for multi-turn conversations
- Usage/stats summaries with --print-stats
- README CLI usage guide + live smoke docs
- E2E test suite covers signal handling, session persistence, and tool calls

**Summary:** First end-to-end binary shipped. Print mode fully functional with settings, flags, session persistence, and signal handling. CLI architecture ready for permission engine, TUI, and RPC modes (phases 8+).

## Phase 8: Compaction & Working Set (2026-06-04)

### Compaction Package (`internal/compact`)
- Implement usage-anchored token estimation (provider tokens; pure chars/4 fallback post-compaction until fresh usage)
- Build Pi-compatible turn serialization (messages + tool results; custom-entry file-list merge)
- Create cut-point selection: never breaks at toolResult; splits on turn boundary; repeats from previous firstKeptEntryId
- Implement 4 ported summarization prompts (user/assistant/tool/mixed-turn context)
- Add LLMSummarizer integration for conversation context during summarization
- Build Compactor state machine (single-entry-per-success guard, prevents duplicate compaction)
- Implement Controller bridge: synchronous persistence (OnMessage hook), 3 compaction triggers (pre-flight once/turn, post-turn, overflow retry-once), pinned mapping, workset persistence via custom{workingset} entry
- 8+ tests: estimator anchor/fallback, cut-point logic, summary generation, controller triggers

### Working Set Package (`internal/workset`)
- Implement FileStamp: mtime+size fast-path, sha256 confirmation for unchanged files
- Add file-freshness tracking: detects disk changes, caps notice at 20 files
- Build ContextBuilder: assembly pipeline (summary → pinned → kept history → staleness notice)
- Implement budget pipeline: trim old >1 KiB tool results (largest-first, outside last 2 turns) → compact once → hard error if overflow persists
- Add custom-entry persistence: WorkingSet saved in session for multi-turn freshness tracking
- Projection-only: context assembly never mutates storage
- 7+ tests: FileStamp staleness, budget pipeline, context assembly

### Agent Loop Enhancements
- Add 3 optional hooks: OnMessage (sync persistence seam), AfterTurn (post-turn trigger), OnContextOverflow (drop failed msg, compact, retry once)
- Implement Agent.Notify for harness event emission
- Integrate FileObserver into tools.Options for read/write/edit recording

### Print Mode Updates
- Move persistence from async event subscriber into OnMessage hook (fixes session-lag race)
- Enable auto-compaction during print mode operation

### Configuration
- Add compaction.disabled kill-switch (bool, default: false)
- Add compaction.reserveTokens (default: 16384) — minimum context budget kept free
- Add compaction.keepRecentTokens (default: 20000) — soft limit on working set

### Test Infrastructure & Quality
- Add scripted Usage field on agenttest.Turn and faux ScriptTurn for compaction trigger testing
- Implement 2 E2E scenarios through RunPrint: overflow recovery and multi-turn persistence
- All 15 project packages pass `-race -count=1`; compact 75.9%, workset 94.4% coverage

**Summary:** Context management complete. Sessions now support automatic compaction under budget pressure, file-freshness awareness, and overflow recovery. Agent loop extensible via hooks. Print mode race condition fixed. Unblocks TUI phase (9+) for UI-driven `/compact` and `/pin` commands.

## Phase 9: Permission Engine (2026-06-05)

### Permission Engine Package (`internal/perm`)
- Implement Mode enum: plan (read-only), prompt (default; read free, rest asks), auto-edit (read+write free, execute asks), yolo (all allowed except explicit denies)
- Build Policy struct with rule parsing: `bash(prefix*)` command glob, `read/write/edit(glob)` path doublestar glob (doublestar-validated), `mcp__server__tool` exact/glob
- Add Rule.Matches: bash commands matched via wildcard prefix, paths matched via doublestar glob relative to cwd, mcp names matched via wildcard
- Implement Engine.Decide decision pipeline: explicit rules (--deny flags > --allow flags > settings deny > settings allow) → baseline screens → session grants → mode defaults → headless asker
- Add baseline safety screens (non-yolo): denyPatterns regex hard-deny for bash; write/edit outside cwd subtree → forced-ask (realpath-resolved); read/grep of secret paths (`**/.env*`, `**/*_rsa`, `**/credentials*`) → forced-ask
- Build session grant store: in-memory per session, generalized from CallInfo (bash: first two tokens + `*`; file ops: directory + `/**`); /permissions listing data structure
- Implement HeadlessAsker: deny with actionable reason (unless mode yolo/auto-edit); denial is LLM-visible IsError tool result
- Add preview generation: edit dry-run unified diff (Myers diff, no apply), write file-size summary, bash command+cwd, mcp pretty-printed args
- Implement Myers line-based diff in diff.go (unified format, ~150 LOC)
- Add CallInfo struct: extract tool name, command, path, category from ai.ToolCall
- Add SandboxSpec unused v1 seam (landlock/seccomp adoption pathway)
- 3 test files: 12 tests covering decision matrix, rule parsing, policy.precedence, secret globs, 90.9% coverage

### Agent Integration
- Add ToolCallFilter interface to hooks.go: sits ahead of BeforeToolCall, fail-closed (error on crash → tool denied)
- Integrate filter chain into toolexec.go: filters run in Config.Filters order before BeforeToolCall hook
- Block decision sent to model as IsError tool result with actionable reason

### CLI Integration (`cmd/mixi`)
- Add buildPermissionEngine: assemble policy from config + flags, build headless asker
- Implement permissionFilter adapter: wraps engine onto ToolCallFilter interface
- Wire filter into agent Config before dispatch
- Add --permission-mode flag, --allow/--deny repeatable flags
- Update e2e_permissions_test.go: 3 scenarios (deny rule blocks write, yolo mode bypasses, secret-glob forced-ask)

### Configuration
- Add PermissionSettings to Settings struct: mode, allow[], deny[], denyPatterns[]
- Deny lists append-only across global/project/extra layers (enforce security invariant)
- ${ENV} expansion on all permission strings (except denyPatterns where regex survives)
- Precedence: --deny flags > --allow flags > settings deny > settings allow > mode defaults

### Behavior Change: Headless Default Mode
- Old (Phase 7): print mode ran every tool call unrestricted (yolo-equivalent) with warning banner
- New (Phase 9): default prompt mode denies write/execute/mcp with actionable reason unless:
  - User passes --permission-mode auto-edit (read+write free), or
  - User passes --permission-mode yolo (all free except explicit denies), or
  - Allow rules in settings/flags permit the call, or
  - Session grant from prior "always" answer exists
- Motivation: safe-by-default headless mode + explicit opt-in for permissive behavior
- Impact: users running `mixi -p "edit file.txt"` now see denial reason instead of silent execution

### Test Results & Quality
- 11 E2E scenarios across perm + agent/loop/toolexec test files
- Full repo: `go test -race` green; perm 90.9% coverage
- Decision matrix tested: every mode × category default verified
- Deny-beats-allow precedence tested
- Edit preview diff consistency tested (== post-execution diff)
- Headless ask→deny behavior tested
- Secret-glob read forced-ask tested
- Symlink escape (realpath resolve) tested

### Key Deliverables
- First-class permission engine gating every tool call in print mode
- Four permission modes with different read/write/execute defaults
- Allow/deny rules with glob syntax for fine-grained control
- Baseline safety screens (bash patterns, writes outside cwd, secret globs)
- Session grants from "always" answers (user confirms once, applies to similar calls)
- Preview diffs for edit approval, command/args summaries for bash/mcp
- Headless asker: deny with actionable reason (actionable for users to add rules)

**Summary:** Permission engine complete. Print mode now enforces permission policies by default (prompt mode), with three other modes for read-only (plan), permissive-file (auto-edit), and unrestricted (yolo). Baseline safety screens catch dangerous patterns. Unblocks TUI phase (10+) for interactive permission approval UI and RPC mode for remote agent control.

## Phase 10: Interactive TUI (2026-06-05)

### TUI Package (`internal/tui`)
- Implement Bubble Tea application (app.go, model.go): root model handles key input, manages transcript, modal, editor, status bar components
- Build event bridge (bridge.go): single goroutine pumps agent.Subscribe() events into tea model via Send() channel; decouples agent goroutines from tea loop
- Implement transcript viewport (transcript.go): sticky-bottom scrolling, component list keyed by message/tool IDs, viewport pagination
- Add message view (msgview.go): renders streaming text → glamour markdown on message end; thinking blocks dim/italic collapsed to first line (+N counter); Shift+Tab toggles expansion
- Add tool view (toolview.go): spinner → ✓/✗ on completion; 8-line preview (Ctrl+O toggles expand); edit/write cards show colorized unified diff (Myers diff); bash/mcp cards show command+args summary
- Implement approval modal (approval.go): overlay with scrollable preview, decision buttons (a/d/A/Esc), focuses input, blocks tool execution until decision delivered to perm.PendingAsk.Reply
- Build input editor (editor.go): textarea with history ring (50 entries), Up/Down cycle history, Ctrl+G suspends tea + execs $EDITOR on temp file + resumes, slash-command autocomplete (prefix match)
- Add status bar (statusbar.go): displays current model, thinking level, context-usage %, estimated $cost, permission mode, background job count; subscribes live usage snapshots
- Implement keymap (keymap.go): bubbles key.Binding table (single source for help text + handlers); 27 bindings covering Enter/Alt+Enter/Esc/Ctrl+C×2/Shift+Tab/Ctrl+P/Ctrl+O/Ctrl+T/Ctrl+G/Ctrl+L/scroll/history
- Add slash commands (commands.go): `/model /compact /pin /tree /permissions /mode /cost /name /quit` (backends exist; `/mcp /new /resume /fork` stub hint notices; `/mcp` awaits phase 11, session-switching awaits user-accepted deferral)
- Implement TUI asker integration (perm/ask.go + tui/): NotifyAsker replaces HeadlessAsker for interactive mode; channels approval requests via perm.PendingAsk; TUI adapter answers modal decisions immediately
- Build E2E test suite (teatest): full interactive session (prompt → stream → tool → approval → result → idle), Esc interrupt mid-stream, double Ctrl+C quit, approval grant persistence across calls
- 15 source files + 3 test files; 74.7% coverage

### Agent Contract Additions
- `Agent.SetModel(ai.Model)` — switch model mid-session (TUI Ctrl+P); snapshot under run() mutex, affects next run only
- `Agent.Model() ai.Model` — query current model
- `Agent.SetThinking(level string)` — set extended thinking level; treated as off if unset (default)
- `Agent.Thinking() string` — query current thinking level
- `Agent.Running() bool` — poll if agent is actively streaming/executing

### Permission Engine Enhancements
- Add `PendingAsk` struct: buffered-1 Reply channel for modal decisions; exactly-once delivery semantics
- Add `NotifyAsker` interface: channel-driven asker (reused by RPC phase 14); bridges perm.Engine to external UI
- TUI builds NotifyAsker and injects via perm.Asker hook before agent dispatch

### CLI Integration (`cmd/mixi`)
- Default to TUI mode when stdin is a terminal (isatty); flag `--mode tui` forces TUI explicit
- Add `run_tui.go`: instantiates app, wires agent.Subscribe, hooks perm.NotifyAsker, runs tea.Program loop
- Redirect slog output to <sessiondir>/mixi.log (never stdout/stderr in interactive mode)
- TUI mode respects same permission settings, session flags, model selection as print mode
- All other behaviors (fork, resume, continue) work identically in TUI

### Dependency Additions
- `github.com/charmbracelet/bubbletea` v1.3.10 (TUI framework)
- `github.com/charmbracelet/bubbles` v1.0.0 (viewport, textarea, spinner)
- `github.com/charmbracelet/lipgloss` (styling, layout, table)
- `github.com/charmbracelet/glamour` v1.0.0 (markdown renderer)
- `golang.org/x/exp/teatest` (E2E test helpers, test-only)

### Test Results & Quality
- `internal/tui`: 74.7% coverage, teatest E2E ×3 scenarios (interactive session, interrupt, quit)
- Full repo: `go test -race ./...` green; 529 total tests, zero flakes
- E2E: prompt → stream → tool execution → approval modal → result → graceful exit; second identical call auto-granted

### Key Behaviors
- **Default mode:** TUI on tty, print on pipe/redirect (auto-detect)
- **Event bridge:** agent runs in background goroutine, pumps events via tea.Send() (non-blocking)
- **Run context ownership:** TUI owns each run's context (created before Prompt goroutine scheduled); Esc closes context before agent.Abort called
- **Modal preview:** sizes to content (prevents title truncation on small terminals)
- **First Shift+Tab:** treats unset thinking level ("") as off, cycles to low
- **Session grants:** 'A' (always allow) stores grant in memory; second identical call bypasses modal
- **Deferred features:** `/new /resume /fork` ship as CLI-flag hint notices; in-TUI session switching deferred (requires runtime rebuild, user accepted 260605)

### Known Limitations
- `/mcp` stubs until phase 11 (MCP client integration)
- In-TUI session switching deferred (would require process-level reinitialization)
- Themes and image preview deferred to future phases (parity with spec only)

**Summary:** Interactive TUI complete. `mixi` with no -p and a tty launches Bubble Tea mode with live transcript, permission approval modal, input editor (history + $EDITOR), status bar, and 27 keybindings. Event bridge decouples agent execution from UI rendering. Session persistence works identically in TUI and print modes. Unblocks MCP client (phase 11) and RPC mode (phase 14) which reuses NotifyAsker.

## Phase 11: MCP Client (2026-06-05)

### Wire Transport Package (`internal/wire`)
- Implement JSONL framing layer for subprocess pipe communication
- Add frame codec: write-side atomic writes with 1MiB per-line cap
- Implement read-side FrameReader with malformed-line detection
- Support optional write deadline for stuck server detection
- 2 source files + tests; shared transport seam for phases 12 (extension host) and 14 (RPC)

### MCP Client Package (`internal/mcp`)
- Implement MCP protocol structs (protocol.go): initialization, tools listing, call/result/error messages
- Support version negotiation: MCP 2025-06-18 with 2025-03-26 fallback
- Build Transport interface abstraction; stdio implementation spawns subprocess with Setpgid (Unix), stderr→DEBUG logs, graceful shutdown SIGTERM→2s→SIGKILL escalation
- Implement Client: id-tracked routing of requests/responses, per-call default 30s timeout, malformed-line cap (10/min) with rate reset
- Build lifecycle state machine (Manager/Supervisor): CONFIGURED→INITIALIZING→READY⇄RESTARTING→FAILED/CLOSED
- Implement exponential backoff (1s/2s/4s), 3-strike disable, /mcp reconnect command support
- Add ServerConfig: mcpServers block decode, ${VAR} env expansion, per-server timeoutMs override
- Implement ToolAdapter: wraps MCP tools as internal/tools.Tool; names as `mcp__<server>__<tool>`, description prefix `[mcp:<server>] `, schema passthrough with permissive fallback (uncompilable schemas don't crash agent)
- 11 source files + 5 test files + testdata fake server; chaos suite vs compiled server, everything-server conformance (13 tools verified live)

### Registry Concurrency (internal/tools)
- Upgrade `tools.Registry` to RWMutex-guarded for safe concurrent registration
- Late MCP tool registration after startup: tools appear next turn, concurrent reads/writes safe

### CLI & TUI Integration (`cmd/mixi`, `internal/tui`)
- Add startMCP: config decode, bounded initial wait, wiring MCP tools post-registry
- Honor --no-mcp flag to skip MCP initialization
- Add `/mcp` TUI command: show server states (name, protocol version, ready state, tool count)
- Add `/mcp reconnect <name>`: manually trigger reconnect attempt
- Implement MCP tool crash handling: mid-call crashes surface as LLM-visible error result "MCP server X crashed during call…"
- Wire MCPFleet manager into TUI for /mcp command dispatch
- Tool adapter integrates MCP tools into permission engine (tool name matches `mcp__*` pattern)

### Configuration
- Extend settings.json: `mcpServers` block with server configs (command, args, env, timeoutMs)
- Example: `{"mcpServers": {"github": {"command": "github-mcp-server", "args": ["stdio"], "env": {"GITHUB_TOKEN": "${GITHUB_TOKEN}"}, "timeoutMs": 30000}}}`

### Test Results & Quality
- Wire: 71.8% coverage; MCP: 86.6% coverage
- Chaos test suite ×3 (simulator, panic, hang scenarios) + e2e ×2 + 3-strike test ×10: no flakes
- Full repo: `go test -race` green
- Code review: 4 findings fixed (ErrConnClosed sentinel, notify-after-FAILED ordering, write deadline for stuck stdin, Manager.mu cleanup)
- Live conformance: everything-server (npx) ran 13 tools, echo round-trip verified

### Key Deliverables
- MCP client with id-tracked request routing and per-call timeouts
- Subprocess lifecycle management (Setpgid spawn, graceful shutdown escalation)
- Tool registration after startup; concurrent-safe Registry for late binding
- Permissive schema fallback (uncompilable schemas don't crash)
- Rate-capped malformed-line detection (10/min)
- /mcp status command in TUI + reconnect support
- CLI --no-mcp flag to disable MCP initialization

**Summary:** MCP client complete. `mixi` now integrates stdio-based MCP servers with per-call timeouts, crash detection, graceful shutdown, and tool lifecycle management. Tools register dynamically post-startup and appear next turn. TUI /mcp command shows server status; CLI --no-mcp flag disables MCP. Unblocks extension host (phase 12) and RPC mode (phase 14) which reuse wire transport and MCP supervisor.

## Phase 12: Extension Host (2026-06-05)

### Extension Host Package (`internal/ext`)
- Implement subprocess JSONL-RPC extension host: discovery from settings block + ~/.mixi/extensions/ + .mixi/extensions/
- Build hello handshake (5s timeout, kill if stuck), validation (tool conflict resolution: built-ins win, first-registered wins)
- Implement per-extension lifecycle: state machine (Starting/Ready/Degraded/Crashed/Disabled), restart backoff (1s/2s/4s), 3-strike disable
- Add event fan-out: per-extension FIFO queue (cap 256, drop-oldest+WARN for non-blocking events), blocking tool_call gate (sequential ordering by registration, 5s response timeout per extension)
- Build protocol version:1 (marked experimental; sign-off phase 16): hello/ready/message types, event subscription (ready, session_start, agent_{start/message_update/message_end/tool_start/tool_end}, agent_end, session_shutdown, notice)
- Implement action API: send_message (steer/followUp/nextTurn), append_entry (session storage), set_status (TUI status bar), notify (notice + stderr in headless), ask_select (headless→first option+WARN), register_command (hello-only)
- Add blocking-gate pipeline: ToolCallFilter integration (sequential ordering), ext tools matched via `ext__*` pattern, ≥3 strike timeouts demote extension to non-blocking
- Build process management: Setpgid spawn (Unix), stderr merge to logs, graceful shutdown (3s grace then SIGKILL group)
- Implement resilience: crash → restart (no mid-flight tool stall), 1MiB wire cap violation → Disabled, 10+ malformed msgs/min → Disabled, isolation prevents extension issues from blocking agent
- Add configuration: settings.json extensions block decode, ${VAR} env expansion, discovery priority
- 11 source files + 6 test files + 2 example extensions; conformance suite (handshake, gate block, mutation chain, timeout strikes, crash/restart/disable, tool round-trip)

### Example Extensions
- `permission_gate.go` (Go): demonstrates tool_call blocking gate; blocks `rm -rf` command end-to-end via gate architecture
- `status_line.sh` (Shell): demonstrates set_status action; updates footer status in TUI

### CLI & TUI Integration (`cmd/mixi`, `internal/tui`)
- Add startExtensions (ext_setup.go): discovery before agent build (ext tools reach registry + system prompt), bounded startup wait
- Wire bindExtensions: connects action API (send_message → agent queues, append_entry → session, set_status → TUI, notify → stderr/TUI notice)
- Add --no-extensions flag to skip extension host initialization
- Integrate ext tools into permission engine (tool names match `ext__*`)
- TUI status bar segments for extension state (Ready/Crashed/Disabled per extension)
- Ext commands appear in autocomplete (command names registered via register_command)
- Event filter sits after permission engine in tool call pipeline

### Test Results & Quality
- `internal/ext`: 6 test files covering conformance (handshake, gate block, timeout strikes, crash restart/disable), tool round-trip, wire cap violation
- CLI e2e: permission_gate blocks rm -rf with yolo mode attempting; --no-extensions skips discovery
- Full repo: `go test -race ./...` green, all tests pass

### Key Deliverables
- Extension host discovers and boots subprocesses with hello handshake
- Blocking tool_call gate prevents extensions from stalling agent (5s timeout, 3-strike disable)
- Action API bridges extensions to agent (send_message), session (append_entry), TUI (set_status/notify/ask_select)
- Crash isolation: extensions restart on crash, wire cap/malformed-msg violations disable gracefully
- Protocol versioned 1 (experimental until phase 16 sign-off)
- Two conformance fixtures (Go + shell examples) demonstrating gate and status_line patterns
- --no-extensions flag for headless/testing mode

**Summary:** Extension host complete. `mixi` now supports subprocess extensions with JSONL-RPC transport, blocking tool_call gates (preventing hung extensions), action API (send_message/append_entry/set_status/notify/ask_select/register_command), crash isolation (restart backoff + 3-strike disable), and permission engine integration. Extensions register tools and commands; tools participate in gate + permission pipeline. Protocol marked experimental pending phase 16 sign-off. Two examples (permission_gate Go + status_line shell) demonstrate core patterns. Unblocks RPC mode (phase 14) which reuses extension architecture for agent-to-agent communication.

## Phase 13: Observability & Replay (2026-06-05)

### Observability Package (`internal/obs`)
- Implement structured JSON logging: slog configured for JSON output → `~/.mixi/logs/mixi-<YYYY-MM-DD>.jsonl` with daily rotation (keeps 7 days)
- Add stderr mirror with configurable level: WARN+ by default; `--verbose` mirrors all records in headless modes (mirrors disabled in TUI)
- Build log-level resolution: `--log-level debug|info|warn|error` flag and `MIXI_LOG` env var with proper precedence
- Implement usage tracking: `Tracker` type accumulates per-turn tokens (input/output), cost estimate, model, duration, tool-call count
- Add `Snapshot.Summary()` shared method for consistent cost/token aggregation across print stats, TUI status bar, and `/cost` command
- Build session replay: `RunReplay(file, options)` reads JSONL session, synthesizes events, renders plain-text transcript with zero API calls
- Implement replay pacing: timestamps from recorded entries, gaps capped 2s, `--speed 1x|5x|instant` controls playback rate
- Add replay truncation: `--until <entryId>` stops rendering at specific entry (useful for stopping mid-session)
- Support read-only lock on live sessions: `replay` mode opens with read-only flag, allows inspection without blocking active session

### Agent Loop Integration
- Emit INFO records on turn lifecycle: turn_id, model, stop_reason, tool_calls count, latency_ms
- Emit INFO records per tool execution: tool name, call_id, is_error flag, latency_ms
- Wire Tracker into event loop via `OnTurnEnd` hook: accumulates usage from provider-returned tokens

### CLI & Mode Integration
- Add `mixi replay <session.jsonl>` subcommand: dispatches before session/API-key validation (replay-only mode)
- Wire log init at start of realMain (before TUI launches)
- Add `session_id` field to all logs after session store opens
- Implement separate EventSink path for print stats: uses Tracker.Totals() instead of per-event snapshots
- Add `/cost` TUI command: shows per-model usage breakdown (tokens, cost, latency)

### Flags & Configuration
- Add `--log-level debug|info|warn|error` (default: warn; env MIXI_LOG overridable)
- Add `--verbose` (mirrors logs to stderr in print/replay; no effect in TUI)
- Add `--speed 1x|5x|instant` (replay pacing; enum validated)
- Add `--until <entryId>` (replay truncation; entry ID prefix-matched)
- `--print-stats` (existing flag) now uses Tracker for consistent cost calculation

### Test Results & Quality
- `internal/obs`: 4 test files covering log schema, level precedence, degrade-on-unwritable-dir, mirror toggle, Tracker folding/consistency, in-flight snapshots, replay golden byte-exact, --until truncation, locked-session replay, pacer capping
- CLI e2e: replay instant/until/missing-file/no-arg, verbose-mirror field check, default-mirror quiet (TUI)
- Full repo: `go test -race` green; obs 83.7% stmt coverage; all phases pass

### Key Deliverables
- Structured JSON logging with daily rotation and configurable stderr mirror
- Per-turn usage tracking: tokens, cost estimate, model, latency; unified across all output channels
- Session replay subcommand: zero-API-call transcript re-render with pacing control
- Read-only session lock: replay can inspect live sessions without blocking
- `/cost` command with per-model breakdown in TUI
- `--verbose` flag for headless log inspection

**Summary:** Observability layer complete. `mixi` now logs structured JSON to daily rotated files (7-day retention), tracks per-turn token usage and costs, and supports session replay with variable pacing. Replay re-renders conversations as plain-text transcripts without API calls; read-only mode allows analysis of live sessions. TUI `/cost` command shows per-model breakdowns. Log levels and stderr mirroring configurable via flags/env. Unblocks OpenAI provider (phase 15) and fault-injection testing (phase 16).
