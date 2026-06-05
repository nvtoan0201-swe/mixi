# Phase 13 Observability & Replay — Sync Report

Date: 2026-06-05 | Plan: plans/260603-1323-mixi-agent-go-coding-agent | Status: ✅ Complete

## Gates
- Tester: DONE (tester-260605-2104 report). Full suite green (464 tests), `-race` clean on obs/agent/modes/tui/config/cmd, 83.7% stmt coverage in internal/obs, all 4 success criteria validated.
- Code-reviewer: DONE_WITH_CONCERNS (code-reviewer-260605-2104 report). No Critical/High. All findings triaged below.

## Review findings resolution
- **M1 (statusbar Snapshot per message end is O(turns²))**: FIXED — reachable via `--max-turns 0` (unlimited). Added `Tracker.Totals()` cheap accessor (no per-turn slice copy); statusbar uses it. `TestTotalsMatchesSnapshot` pins equivalence with the full snapshot.
- **L1 (log file named at startup; midnight-crossing session keeps yesterday's file)**: accepted v1 — rotation is per-process by design; documented at `openDaily`. >1-day single sessions out of scope.
- **L2 (replay time.Sleep uninterruptible)**: accepted — gaps capped at 2s, SIGINT kills the process.
- **L3 (multiHandler Enabled/Handle semantics)**: reviewer verified correct, no action.
- **L4 (cmd e2e reads ../../internal/obs/testdata)**: accepted — single shared fixture beats a third copy; `os.Stat`+`t.Fatal` guard fails loud if moved.
- **L5 (--log-level error + --verbose interaction)**: reviewer verified correct (mirror = max(WARN, level) default; --verbose pins mirror to level).

## What shipped
- `internal/obs` (new): `log.go` (slog JSON → `~/.mixi/logs/mixi-<date>.jsonl`, prune keep 7, ts-renamed time key, optional stderr mirror WARN+ or full with `--verbose`, `--log-level`/`MIXI_LOG` resolution), `usage.go` (Tracker: per-turn {turn, model, usage, durMs, toolCalls}, session totals + byModel + lastContext; `UsageSink` interface per design §5#9; shared `Snapshot.Summary()`), `replay.go`+`replay_render.go` (`mixi replay <file> [--speed 1x|5x|instant] [--until <id>]`, read-only no-flock open, entry→event synthesis, plain-text transcript, paced via recorded timestamps capped 2s/gap).
- Wiring: log init first in main (TUI gets no mirror; `slog.SetDefault` catches package-level call sites), `session_id` field after open, component tags (agent/compact/mcp/ext), replay dispatch before session/API-key requirements. Print stats + TUI statusbar + `/cost` (now with per-model breakdown) all derive from one Tracker.
- Agent loop INFO records: turn start/end (turn, model, stop_reason, tool_calls, latency_ms), tool exec (tool, call_id, is_error, latency_ms) — closes the field-spec sweep.
- Flags: `--speed`, `--until`, `--verbose` (+ enum validation). Removed: old `logWriter`/`newLogger` (sessions-dir mixi.log replaced by daily obs file).
- Tests: obs unit suite (schema, mirror levels, prune, level precedence, degrade-on-unwritable-dir, tracker folding/consistency/in-flight, golden replay byte-exact, --until, locked-session replay, pacer), cmd e2e (replay instant/until/missing-file/no-arg, verbose-mirror field check, default-mirror quiet).

## Plan sync
- phase-13 frontmatter → completed; all 4 success criteria checked with test references.
- plan.md row 13 → ✅ Complete (260605) with midnight-rotation + TUI-replay-non-goal notes.
- Remaining: 14 (RPC), 15 (OpenAI provider), 16 (hardening). Phase 14 consumes `Tracker.Snapshot()` for `get_session_stats`.

## Verification
- `go build ./...`, `go vet ./...` clean. Full `go test ./...` green. `-race` green on obs/agent/modes/tui/config/cmd/mixi (post-fix re-run included).

## Unresolved questions
- None blocking. Phase 16 reminder: protocol:1 sign-off (phase 12) and status_line.sh assertion still parked there.
