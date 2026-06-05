---
phase: 13
title: "Observability & Replay"
status: completed
priority: P2
effort: "2d"
dependencies: [6, 7]
---

# Phase 13: Observability & Replay

## Overview
`internal/obs`: slog JSON logging to rotated files, per-turn/per-session usage+cost tracking surfaced in TUI/`/cost`/print stats, and `mixi replay <session.jsonl>` mode re-rendering recorded sessions with zero API calls.

## Context Links
- Brainstorm §14 (logging fields/levels, UsageTracker, replay spec — implement exactly), §5 #9 (UsageSink)
- Research §13 item 13 (Pi gap)

## Requirements
- Logging: JSON handler → `~/.mixi/logs/mixi-<date>.jsonl`, daily rotation keep 7; std fields ts/level/component/session_id/turn (+tool, call_id, model, latency_ms, attempt, err); TUI mode never stdout/stderr; print/rpc → stderr WARN+ unless `--verbose`; `--log-level` + `MIXI_LOG` override.
- UsageTracker: subscribes agent events; per-turn records {turn, model, usage, durMs, toolCalls}; session aggregate + byModel; feeds statusbar, `/cost`, `--print-stats`, RPC `get_session_stats` (phase 14 consumes).
- Replay: `mixi replay <file> [--speed 1x|5x|instant] [--until <entryId>]`; read-only open (no flock); entries → synthetic event sequence → print renderer (tty: simple paced text render; full TUI replay = non-goal v1).

## Related Code Files
- Create: `internal/obs/log.go`, `usage.go`, `replay.go` + tests
- Modify: `cmd/mixi/main.go` (replay subcommand, log init first), all components' slog component tags (mechanical sweep), `internal/tui/statusbar.go` (tracker snapshots)

## Implementation Steps
1. `log.go`: handler setup, rotation (size-agnostic daily file naming, prune >7), component-tagged child loggers helper.
2. `usage.go`: tracker w/ mutex'd aggregates; snapshot API for UIs; tests over scripted event streams.
3. `replay.go`: entry→event synthesis (message entries → start/end pairs w/ recorded content; tool results paired to calls), pacing, `--until` cut.
4. Sweep: replace ad-hoc logging from earlier phases with component loggers (grep for fmt.Fprintf stderr leftovers).
5. Replay golden test: phase-6 example session renders to expected text transcript.

## Success Criteria
- [x] Log lines validate against field spec (schema test on captured output) — `TestLogSchema` (obs), `TestE2EVerboseMirrorCarriesStandardLogFields` (cmd/mixi, end-to-end)
- [x] `/cost` + `--print-stats` figures equal sum of recorded message Usage (consistency test) — `TestSnapshotConsistency`; both UIs derive from one `obs.Tracker` Snapshot/Summary
- [x] `mixi replay testdata/example.jsonl --speed instant` reproduces golden transcript, exits 0, no network — `TestReplayGolden` (byte-exact vs `example_replay_transcript.golden`), `TestE2EReplayInstant`; `RunReplay` structurally never touches `internal/ai`
- [x] TUI run produces zero bytes on stdout/stderr outside tea (guard from phase 10 still green) — TUI gets no stderr mirror (`mirror=nil`), `TestNoMirrorFileOnly`; `slog.SetDefault` routes package-level call sites to the file too

## Risk Assessment
- Logging sweep touches every package → mechanical, do last in phase; keep diff review-able per package.
