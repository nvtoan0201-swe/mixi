# Code Review — Phase 13: Observability & Replay

Date: 2026-06-05 | Reviewer: code-reviewer | Scope: uncommitted working tree vs HEAD

## Scope
- New pkg `internal/obs`: log.go, usage.go, replay.go, replay_render.go + 3 tests + testdata (2 fixtures)
- `cmd/mixi`: replay.go (new), main.go, e2e_replay_test.go (new), e2e_logging_test.go (new)
- Touchpoints: config/{flags,runtime}.go, modes/print.go, tui/{statusbar,model,commands}.go, agent/{loop,toolexec}.go
- LOC: obs core ~566; all <200/file. Build clean, vet clean, all affected pkg tests pass, `-race` clean on obs+modes.

Note: actual working tree is narrower than the task brief listed (no changes to internal/config/config.go, internal/agent/events.go, internal/mcp/*, internal/tui logo/scripts). Reviewed what is actually modified.

## Overall Assessment
Solid, idiomatic, well-tested. Sealed-union switches, table-driven tests, package-prefixed errors, files under 200 LOC — matches house style. All four acceptance criteria met (verified below). No Critical/High issues. A few Low/Medium observations, all v1-acceptable; none blocking.

## Acceptance Criteria — Verified

1. **Log lines validate against field spec** — MET. `obs/log_test.go:TestLogSchema` asserts ts/level/msg/component/session_id present, `time` key renamed→`ts` (renameTimeToTS, log.go:66), event-specific tool/call_id/turn/latency_ms wired in loop.go:96/121 + toolexec.go:186. `e2e_logging_test.go` confirms component=agent/session_id=/turn=1 end-to-end through the mirror.
2. **/cost + --print-stats equal recorded Usage sum** — MET. Both derive from `Snapshot.Summary()` (single source). `usage_test.go:TestSnapshotConsistency` asserts `snap.Total == plain sum`. print.go:82 and commands.go:cmdCost both call Snapshot.
3. **replay reproduces golden, exit 0, zero API** — MET. `replay_test.go:TestReplayGolden` byte-exact; `e2e_replay_test.go:TestE2EReplayInstant` runs full binary with no provider key (zero network is structural — RunReplay never touches ai/), exit 0, stderr empty.
4. **TUI zero bytes on stdout/stderr outside tea** — MET. main.go:97-100 passes `mirror=nil` for ModeTUI; `obs.Setup` then builds file-only handler. `log_test.go:TestNoMirrorFileOnly` proves even ERROR stays off terminal. slog.SetDefault wired to same logger so package-level call sites (session crash recovery) obey the policy.

## Regression / Blast-Radius Checks — Pass

- **Agent loop event ordering**: loop.go adds only two `Log.Info` calls + a `time.Now()`; no `emit` reordering. EvTurnStart/EvTurnEnd emit positions unchanged. Tracker depends on EvTurnStart preceding EvMessageEnd preceding EvTurnEnd — matches documented order (events.go:16-22) and agent tests still pass.
- **Print-mode exit codes**: print.go switch on EndReason unchanged (ExitRunErr/ExitSIGINT/ExitOK). Only `usageStats`→`obs.Tracker` swap; `--print-stats` output format byte-identical (Summary() string copied verbatim from old `print()`).
- **Statusbar on error turns (zero-usage assistant)**: statusbar.go now feeds every event to `s.usage.Observe`, refreshes ctx/cost only when `snap.LastContext > 0`. Zero-usage turn leaves LastContext=0 → cost/ctx unchanged. `usage_test.go:TestErrorTurnWithoutUsageIgnored` locks this. Prior behavior preserved (old code also gated on `Usage.Total != 0`).
- **Replay read-only vs live flock**: RunReplay uses `session.Options{ReadOnly:true}` → O_RDONLY, no flock (loader.go:22-35) — same established path as manager.go:85 branch source. `replay_test.go:TestReplayWhileSessionLocked` proves replay works against a session another process holds locked. ReadOnly also skips tail-truncate/newline-repair (loader.go:77,85) so replay never mutates a live file — correct.

## Public Contract Changes (additive, non-breaking)

- New flags `--verbose`, `--speed`, `--until` (flags.go); `--speed` enum-validated. `RuntimeConfig.Verbose` added. All additive.
- New subcommand `mixi replay`, new Mode `ModeReplay`. Exit codes reuse existing ExitOK/ExitUsage.
- `sortedKeys` generalized to generic `sortedKeys[V any]` (statusbar.go) — internal, callers unaffected.
- No change to JSON sink wire format, session schema, or agent.Event union. ✓

## Findings

### Medium

**M1 — Status bar Snapshot is O(turns²) per session.** statusbar.go `observe` calls `s.usage.Snapshot()` on every EvMessageEnd; Snapshot (usage.go:116) deep-copies the full turns slice each time. Over a session of N turns that is O(N²) allocations. Concrete failure mode: only material if max-turns grows large (default cap 80 → ~3k copies worst case, negligible). **Verdict: acceptable for v1** given the default turn cap; flag if max-turns default is later removed/greatly raised. Cheap future fix: have Snapshot expose Total/LastContext without copying turns, or cache last figures. Not blocking.

### Low

**L1 — Daily log file fixed at startup; midnight-crossing run keeps writing yesterday's file.** openDaily (log.go:85) stamps the name once. A run spanning midnight logs to the prior day's file. Real failure mode: cosmetic (records land one file early); pruning still keeps 7 newest by name. **Acceptable for v1** (long-lived single runs rare for a coding-agent CLI). Documented intent matches.

**L2 — Replay `time.Sleep` pacing is uninterruptible (no ctx).** replay.go:97 `pacer.sleep`. A 1x replay of a long session can't be cancelled except by SIGINT killing the process. **Acceptable v1** — capped at maxReplayGap=2s per gap (replay.go:72), and `--speed instant` exists for fast paths. SIGINT terminates anyway. Note for v2 if interactive replay control is wanted.

**L3 — `multiHandler` semantics double-check `Enabled` in Handle.** log.go:152 re-checks `hh.Enabled` inside Handle before dispatching — correct and necessary (file at DEBUG + mirror at WARN must filter per-handler since the parent `Enabled` returns true if *any* handler accepts). WithGroup/WithAttrs fan out correctly. Record `Clone()` per handler avoids attr-buffer aliasing. **No issue — verified correct against slog contract.**

**L4 — e2e replay tests reference `../../internal/obs/testdata` via filepath.Abs.** e2e_replay_test.go:14 reaches across packages into obs testdata, with `os.Stat` guard + `t.Fatal` on miss. Brittle if obs testdata moves/renames, but the Stat guard fails loudly (not silently) and the path is the canonical phase-6 example fixture. **Low** — could copy fixture into cmd/mixi/testdata for isolation, but DRY argues against duplicating. Acceptable; the guard makes breakage obvious.

**L5 — Mirror level logic: `--log-level error --verbose`.** log.go:48-51: `lv=Warn; if Verbose || Level>Warn { lv=Level }`. With Level=Error+Verbose, mirror=Error (Verbose can't *lower* below an explicit stricter level). With Level=Debug+Verbose, mirror=Debug. Matches documented "Verbose lowers the mirror threshold from WARN to Level" — Verbose never *raises* noise above what the level permits. **Correct, no issue.** `e2e_logging_test.go` covers verbose-info and default-quiet paths.

## Concurrency — Verified Safe

- Tracker mutex guards all field access; Observe (TUI Update loop / print goroutine) vs Snapshot (slash commands / print main) is data-race-free. `-race` run on obs+modes clean.
- Print-mode goroutine (print.go:57) writes `endReason` then returns *before* main reads it and calls Snapshot — goroutine has fully exited, no concurrent Tracker access at Snapshot time. Safe.
- `slog.SetDefault` (main.go:109) is a one-time process-global set before any subsystem starts; not re-entered. No interference within a single process. (Test interference: each test that needs isolation builds its own logger via `obs.Setup` rather than the default — log_test does this; not a concern.)

## Pattern Adherence — Pass
Sealed-union exhaustive switches (entryEvents, transcript.Emit), table-driven tests, `fmt.Errorf("replay: …%w")` package-prefixed wrapping, generic `sortedKeys`, injectable clock (`now func() time.Time`) for duration tests. No plan-artifact references in comments (checked log.go/usage.go/replay.go — comments explain the *why*, not phase numbers). Files all <200 LOC.

## Positive Observations
- Single-source-of-truth for figures (`Snapshot.Summary()`) eliminates the /cost vs --print-stats drift the criteria worry about.
- Logging degrades gracefully on unwritable dir (Setup → DiscardHandler/mirror-only) — "logging must never break a run" honored, tested.
- Replay synthesizes the *same* event sequence a live run emits, reusing a transcript renderer that mirrors the live print renderer — replay can't diverge from live semantics by construction.
- Read-only replay never mutates a live session file (skips truncate/newline-repair) — correct and tested against a held lock.
- In-flight turn included in Snapshot so live UIs never under-report (tested).

## Metrics
- Build: clean. Vet: clean. Tests: obs/cmd/modes/tui/agent/config all pass; `-race` clean on obs+modes.
- New tests: obs (3 files, ~13 cases) + cmd e2e (replay 4 cases, logging 2 cases). Good coverage of field spec, consistency, in-flight, error-turn, off-path, locked-session, pacing.

## Unresolved Questions
1. M1 (Snapshot O(N²)): acceptable only while a turn cap exists. Is max-turns guaranteed to stay bounded, or is unbounded mode planned? If unbounded, recommend a non-copying Total/LastContext accessor before then.
2. L1 (midnight rotation): confirm long-running (>1 day) single sessions are out of scope for v1 — if not, rotation should re-evaluate the date on write.
