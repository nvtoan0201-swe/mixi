---
phase: 10
title: "Interactive TUI"
status: completed
priority: P1
effort: "6d"
dependencies: [7, 8, 9]
---

# Phase 10: Interactive TUI

## Overview
Bubble Tea interactive mode: streaming transcript, thinking folds, tool cards, approval modal (TUI Asker), status bar, keymap, slash commands, input history, $EDITOR escape. The event bridge decouples agent goroutines from the tea loop.

## Context Links
- Brainstorm §15 (bridge code pattern, rendering spec, full shortcut table, slash commands — implement exactly)
- Research §10 (Pi TUI behaviors for parity reference)

## Requirements
- Deps: bubbletea, bubbles (viewport/textarea/spinner), lipgloss, glamour.
- Bridge: single goroutine pumping `Agent.Subscribe()` → `p.Send(agentMsg{ev})`; approval via `AskRequest.Reply` channel (perm engine blocks, modal answers).
- Rendering: live raw text during stream, glamour markdown on message end; thinking dim-italic collapsed to first line (+N, ctrl+t); tool cards w/ spinner→✓/✗, 8-line preview, ctrl+o expand-all; edit/write cards show colorized diff; approval modal w/ scrollable preview, a/d/A/Esc; status bar `model • thinking • ctx% • $cost • mode • jobs`.
- Keymap per brainstorm §15 table (Enter steer-or-send, Alt+Enter follow-up, Esc interrupt, double Ctrl+C quit, Shift+Tab thinking, Ctrl+P model, Ctrl+G $EDITOR, history on Up/Down, PgUp/PgDn scroll).
- Slash commands: `/model /compact /new /resume /fork /tree /pin /permissions /mode /mcp /cost /name /quit` (those whose backends exist; `/mcp` stub until phase 11; registered-command plumbing extension-ready for phase 12).
- Never write logs to stdout/stderr in TUI mode (slog → file only, wired fully in phase 13; this phase sets the guard).

## Related Code Files
- Create: `internal/tui/app.go`, `bridge.go`, `model.go`, `keymap.go`, `transcript.go`, `editor.go`, `msgview.go`, `toolview.go`, `approval.go`, `statusbar.go` + tests
- Modify: `cmd/mixi/main.go` (default mode = TUI when tty), `internal/perm/ask.go` (TUI Asker impl)

## Implementation Steps
1. `bridge.go` + `model.go`: root model, agentMsg envelope, exhaustive event switch (default: status refresh).
2. `transcript.go`: viewport with sticky-bottom logic, component list keyed by message/tool ids.
3. `msgview.go`/`toolview.go`: rendering states per spec; ApplyStreamEvent delta append.
4. `approval.go`: modal overlay, focus capture, decision → Reply chan; timeout = none (user decides).
5. `editor.go`: textarea, history ring (50), slash autocomplete (prefix match over command table), Ctrl+G external editor (suspend tea, exec $EDITOR on temp file, resume).
6. `keymap.go`: bubbles/key bindings table = single source for help + handlers.
7. `statusbar.go`: subscribes context-usage + usage tracker snapshots.
8. Tests: model Update unit tests with synthetic event sequences (teatest); golden View() snapshots for msgview/toolview states; approval flow test (decision unblocks fake engine).

## Success Criteria
- [x] Full interactive session against fake provider in teatest: prompt → streaming → tool card → approval modal → result → idle
- [x] Esc interrupts mid-stream; double Ctrl+C quits; session file correct after quit
- [x] Approval modal blocks tool execution until decision; 'A' adds session grant (second identical call auto-allowed)
- [x] No stdout writes outside tea renderer (guard test)

## Completion Notes (260605)
- Asker seam: `perm.NotifyAsker` (channel asker in perm, reused by RPC later); cmd adapter `agentNotifier` breaks the engine-before-agent construction cycle; modal answers over `PendingAsk.Reply` (buffered, exactly-once).
- Run interruption: the UI owns each run's context (`startRun` creates it before the prompt goroutine schedules) — closes the Esc-before-`Agent.Abort`-armed race found in teatest.
- Modal sizes its preview to content; a fixed-height preview pushed the title past the terminal height (tea truncates the frame).
- Session-switch commands (`/new /resume /fork`) ship as hint notices pointing at CLI flags — in-TUI switching needs a process-level runtime rebuild, deferred (user-accepted 260605). `/mcp` stubs until phase 11.
- Agent gained `SetModel/Model/SetThinking/Thinking/Running`; `run()` snapshots config under the lock so mid-session switches only affect the next run. First Shift+Tab from unset thinking level fixed to cycle to low.
- TUI logs go to `<sessiondir>/mixi.log` (or discard), never stdout/stderr; full slog wiring stays phase 13.
- Tests: tui pkg 74.7% cover, teatest E2E ×3 no flakes, full repo `-race` green (529+ tests).

## Risk Assessment
- Largest UI surface; scope-creep risk → defer polish (themes, image preview) — parity with spec only.
- glamour re-render cost on long transcripts → render markdown once per completed message, cache strings.
