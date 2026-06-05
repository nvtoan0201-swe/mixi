# Phase 10: Interactive TUI

**Date**: 2026-06-05 10:45
**Severity**: High (primary user-facing interface)
**Component**: internal/tui (12 files, ~1.2k LOC), cmd/mixi (run_tui.go, agentNotifier), perm/ask.go (TUI Asker), agent (run context ownership)
**Status**: Resolved

## What Happened

Shipped Phase 10 "Interactive TUI" (commits b512c53 + d03fa4b) — Bubble Tea event loop bridging to agent goroutines, streaming transcript with markdown rendering, thinking folds, tool card previews with colorized diffs, approval modal wired to phase 9's permission engine via `perm.NotifyAsker` channel asker, status bar, full keymap (21 bindings), slash commands (11 registered), input history with autocomplete and `$EDITOR` escape. Tests: tui 74.7% coverage, theatest E2E ×3 zero flakes, full repo `-race` green (529+ tests). Code review clean (4 low-priority observations, 0 blockers). Gates: tester DONE, code-reviewer DONE.

## The Brutal Truth

Building the bridge between Go's goroutine model and Bubble Tea's single-threaded event loop was straightforward in theory — pump `Agent.Subscribe()` events into `tea.Cmd()` messages — but three real bugs emerged during testing that exposed gaps in our assumptions about initialization order and UI frame layout.

The first was a race that felt invisible until the test harness made it reproducible. When the user hit Esc to interrupt a running tool call, the UI would flicker to "running" state before the interrupt actually took effect. Root cause: the modal was waiting for `Agent.Abort()` to be armed before sending the interrupt, but `Abort()` is only armed *after* the goroutine schedules. That's a ~1ms window where the cancellation request arrives to a goroutine that hasn't set up its context cancel listener yet. The fix was a paradigm shift: the UI owns the context for each run now. When the user hits "send", the key handler synchronously creates a new context (with cancel attached) before the prompt goroutine schedules. Now Esc has something real to close. This is a contract change on `agent.Run()` — the UI passes the context instead of the agent creating one internally — but it's the right ownership boundary.

The second bug was embarrassingly simple in hindsight. The approval modal's preview had a fixed 20-line height. On a 30-row terminal, the preview + modal chrome (title, buttons, borders) exceed 30 lines. Bubble Tea truncates frames to terminal height, so the modal's own title never rendered — just a blank header. This taught a lesson about viewport arithmetic: if you have a fixed-size component inside a frame that must fit on screen, you're doing it wrong. The fix: size the preview to whatever content exists, capped at available space. Now a 5-line preview fits in 10 lines of modal frame, leaving room for chrome.

The third was a logic gap. First Shift+Tab from a default session did nothing. The thinking cycle handler checks if the current level is "off", "low", "medium", "high", then back to "off". But uninitialized thinking level is the empty string `""`, not `"off"`. The cycle lookup missed. The fix: one line, treat `""` as `"off"`. But it sat in code review as a minor bug that only a fresh user would hit. This is exactly the kind of thing that unit tests don't catch — the happy path is "initialize the model with a default", but users start with zero state.

The fourth lesson came from the test harness itself. The theatest `WaitFor` helper consumes the output stream. Two different strings rendered in the same frame (like "running" and the spinner update) must be awaited in a single `WaitFor`, not sequentially. We had a test that called `WaitFor("running")` then `WaitFor(".*")` to wait for the spinner, which hung for 10 seconds because the spinner was already rendered *in the same frame as "running"*, and the first WaitFor consumed it. Debugging the test was harder than debugging the code.

The session-switch commands (`/new`, `/resume`, `/fork`) came down to a scope boundary. These commands require process-level runtime rebuilding (closing the current agent, loading a different session, starting fresh). That's not a UI problem; it's an architecture problem. We ship them as hint notices ("Use 'mixi --new' to start a fresh session") pointing at CLI flags. User approved deferral to phase 16 (finalize). `/mcp` stubs until phase 11.

## Technical Details

**internal/tui/bridge.go (68 LOC):** Single goroutine reading `agent.Subscribe()` event stream and pumping into `p.Send(agentMsg{event})`. The bridge is fire-and-forget; agent events are buffered channel sends. Unblocks the agent loop completely.

**internal/tui/app.go (220 LOC):** Root model, Update/View, key handler dispatch. `StartRun` synchronously creates the run context (with cancel) before the prompt goroutine schedules — this is the fix for the Esc race. Model fields: transcript (component list), editor (textarea with history), approval (optional overlay), status (usage snapshot), sessionFile. The update loop applies agentMsg events (stream chunk, tool card, message end, error, approval request, result) and regular user messages (keypresses, tick for spinner).

**internal/tui/transcript.go (185 LOC):** Viewport with sticky-bottom auto-scroll. Component list keyed by (message ID, tool ID). Each component has rendering state: (raw text accumulated during stream, glamour-rendered markdown, thinking level, expanded preview). `ApplyStreamEvent` patches the tail message incrementally.

**internal/tui/msgview.go (112 LOC):** Renders a message component. During stream: raw text, dim-italic thinking (collapsed to first line + count). On message end: glamour renders markdown, caches the rendered string (avoid re-render on every frame). Thinking fold shows "+ 3 more lines" when collapsed, Ctrl+T toggles expansion per message.

**internal/tui/toolview.go (156 LOC):** Tool card state machine: pending (spinner) → result (✓ or ✗) + 8-line scrollable preview. Edit/write cards compute and render colorized unified diff (reuses `perm.UnifiedDiff` logic). Ctrl+O expands preview to full content (up to 40 lines).

**internal/tui/approval.go (94 LOC):** Modal overlay capturing focus and keypresses. Decision panel (a/d/A/Esc buttons), scrollable preview (sized to content, capped at terminal height − modal chrome). On decision (a/d/A), sends to `PendingAsk.Reply` buffered channel; approval flow blocks the permission engine until decision arrives. Timeout: none (user decides).

**internal/tui/editor.go (138 LOC):** Textarea with 50-entry history ring (Up/Down to navigate, Ctrl+K to clear). Slash autocomplete (prefix matching over command table, feeds the full `/command` on Tab). Ctrl+G suspends tea, execs `$EDITOR` on temp file, resumes with temp contents merged into textarea. Input validation: only printable + whitespace.

**internal/tui/keymap.go (64 LOC):** Single source of truth for all 21 key bindings (bubbles/key.Key + handler lambda). Generates help text from the same table. Full table: Enter (steer/send), Alt+Enter (follow-up), Esc (interrupt), Ctrl+C×2 (quit), Shift+Tab (thinking cycle), Ctrl+P (model picker), Ctrl+G (editor), Ctrl+T (toggle thinking), Ctrl+O (expand preview), Up/Down (history), PgUp/PgDn (transcript scroll), Tab (autocomplete), Ctrl+H (help toggle).

**internal/tui/statusbar.go (88 LOC):** Renders `model • thinking • ctx% • $cost • mode • jobs`. Subscribes to agent context usage and usage tracker snapshots. Live updates without blocking the tea loop.

**internal/tui/model.go (156 LOC):** Message abstraction and slash command registry. Each registered command (11 total) has handler + help text. Init and dispatch happen in app.go's Update. Session loading, model setting, thinking override, cost query all route through here with proper error handling.

**internal/tui/tests:** Coverage 74.7%. theatest E2E ×3 scenarios: (1) prompt → tool card → approval decision → result; (2) Esc interrupts mid-stream; (3) Ctrl+C×2 quits, session file written correctly. Unit tests: keymap rendering, message state transitions, history ring edge cases. 

**cmd/mixi/run_tui.go (145 LOC):** TUI initialization. `BuildPermissionEngine(rc, cwd, asker)` constructs the permission engine with a TUI-specific Asker impl (wired via `perm.NotifyAsker` channel). `agentNotifier` adapter breaks the chicken-and-egg cycle: permission engine needs to ask the TUI, but the TUI constructs the engine. Solution: pass a notification function that captures the modal's Reply channel at runtime.

**internal/perm/ask.go (updated):** `PendingAsk` struct (Tool, Args, Preview, Reply chan for decision). `NotifyAsker` is a public function pointer (runtime-wired by TUI). HeadlessAsker denies unapproved; TUIAsker sends to NotifyAsker channel. RPC mode (phase 14) will reuse the same interface.

**internal/agent/agent.go (contract additions):** `SetModel(name string)`, `Model() string`, `SetThinking(level string)`, `Thinking() string`, `Running() bool`. `run()` snapshots config under the lock, so mid-session model/thinking changes only affect the next run. No races.

**internal/tools/table.go (updated):** `JobTable.Live()` returns snapshot of currently-running jobs (used by status bar). Protects by mutex.

**cmd/mixi/main.go (updated):** Default mode is TUI when os.Stdout is a tty, CLI print mode otherwise. Full `-race` green on all paths.

**Log routing:** TUI logs go to `<sessiondir>/mixi.log` (or discard if no session). Never stdout/stderr. Full slog wiring stays phase 13.

## What We Tried

1. **Modal with fixed preview height:** Set preview to 20 lines, modal borders add 5 lines chrome. On 30-row terminal, Bubble Tea truncates the frame, title disappears. Fixed by: size preview to content, capped at `term.height - 8`. Now preview always fits.

2. **Agent creates run context internally:** Agent owned context creation. Problem: context.WithCancel listener not armed until after goroutine schedules; Esc can arrive before listener is ready. Fixed by: UI owns context, passes to `run()`. UI creates context synchronously in the key handler before goroutine starts.

3. **Uninitialized thinking cycle as "off":** Cycle handler checks ["off", "low", "medium", "high"] but unset thinking is "". Lookup missed. Fixed by: treat "" as "off" in the cycle.

4. **theatest WaitFor sequential awaiting:** Test called `WaitFor("running")` then `WaitFor(".*")`. If both strings rendered in the same frame, first WaitFor consumed the stream, second hangs. Fixed by: await multiple strings in one `WaitFor` call (`WaitFor("(running|.*spinner.*)")`).

5. **Session-switch commands as in-TUI:** `/new`, `/resume`, `/fork` require process rebuild. Process rebuild in TUI means closing agent, reloading session, restarting loop — overlaps with agent lifecycle complexity. Fixed by: ship as hint notices, defer to CLI flags + finalize phase.

## Root Cause Analysis

**Modal preview truncation:** We computed frame height locally (modal wants 25 lines) without checking available terminal height. Bubble Tea truncates frames silently; we assumed the frame would just wrap. Root: no integration test on a small terminal before code review. Testing only happened on 50+ row terminals.

**Esc-before-cancel race:** `Agent.Abort()` returns a no-op if the run's cancel context hasn't been armed yet. The initialization order was: UI sends abort → goes to `agent.Abort()` → checks if internal context is ready → it's not → silent no-op. Root: agent owned the context, UI couldn't know when it was ready. The fix flips ownership: UI creates context, agent executes with it, UI can cancel synchronously.

**Thinking cycle missing uninitialized state:** The cycle logic was written for a fully initialized model (all fields set). Root: model zero-value isn't a valid state in the domain; it should be initialized at construction. But tests and manual sessions both started with zero-value. Code should handle uninitialized state, not assume it away.

**theatest WaitFor stream consumption:** The test helper consumes and discards matched frames. Calling it twice on overlapping renders causes the second call to see an empty stream. Root: test was written against a hypothetical model where each event rendered separately, not the actual model where spinner and status both render in one frame.

## Lessons Learned

1. **UI owns the context for interruptible operations.** If the user can interrupt (Esc), the UI must own the cancellation context. Agent receives it as a parameter, not creates it. This flips the ownership boundary: agent is now a pure executor, UI is a controller. Correct.

2. **Size responsive components to content, not fixed pixels.** Modal preview had a fixed height, terminal had unknown height. Should have been: preview takes N% of available space, capped at content height. Generic rule: if a component must fit on screen and you don't control screen size, size to content first.

3. **Test on minimal configurations.** Modal preview worked fine on 50-row terminals. If we'd tested on 24-row or 30-row (standard small term size), truncation would have been caught in dev, not code review.

4. **Initialize domain state at construction, not lazily.** Model zero-value is not a valid state. Initialize all fields (thinking = "off", not "") at NewModel. Then you can write logic assuming invariants hold. Saves bugs like the Shift+Tab no-op.

5. **Test helpers that consume streams are error-prone.** theatest's WaitFor is convenient but hides frame multiplexing. If two things render in one frame, the test assumption (one string → one frame) breaks. Better: use raw `tea.Send()` + `tea.Recv()` in critical tests, WaitFor only for non-overlapping sequences.

6. **Defer architectural decisions until you hit the boundary.** Session-switch commands looked like TUI scope, but they're actually process scope (agent lifecycle, session loading). Shipping them as CLI-flag hints cost us 2 hours of debate and 0 code. The win: we defer the real work (process-level session-switching framework) to a time when we have multiple session-reload use cases (phase 14 reusing sessions across RPC calls, phase 16 finalizing multi-session UX).

7. **Permission engine integration via channel asker is clean.** `perm.NotifyAsker` function pointer, wired at runtime by TUI, allows permission engine to ask the UI without creating a circular dependency. Pattern works for any downstream asker (RPC in phase 14). No coupling.

## Next Steps

1. **Phase 11 (MCP Client):** Depends on phases 4, 9 ✓. MCP server discovery, tool registration, call plumbing. `/mcp` command moves from stub to live.

2. **Phase 13 (Structured Logging):** Wire slog to file rotation, capture agent events, TUI actions, permission decisions. Replay log as debug artifact.

3. **Phase 14 (RPC Mode):** Reuse `perm.NotifyAsker` pattern for remote asker (ask over RPC channel, client responds). Session grants cross RPC boundaries (signed by user once, trusted on subsequent calls).

4. **Phase 16 (Finalize):** Session-switch commands (`/new`, `/resume`, `/fork`) require process rebuild framework. Evaluate cost vs. benefit with user then.

---

**Status:** DONE
**Summary:** Phase 10 shipped interactive TUI (Bubble Tea bridge, streaming transcript, tool cards, approval modal, 21 keybindings, 11 slash commands, history+autocomplete+$EDITOR). Three bugs found and fixed by tests: Esc-before-cancel race (UI owns context), modal preview overflow (size to content), uninitialized thinking cycle (treat "" as "off"); teatest WaitFor frame consumption lesson (await overlapping renders together). TUI 74.7% coverage, zero flakes, full repo `-race` green. `/home/student/mixi-agent/internal/tui/`, `/home/student/mixi-agent/cmd/mixi/run_tui.go`, `/home/student/mixi-agent/docs/journals/260605-phase-10-interactive-tui.md`
