# PM Report: Phase 8 Complete — Compaction & Working Set

**Plan:** `plans/260603-1323-mixi-agent-go-coding-agent/` | **Date:** 260604 | **Branch:** master

## Status

| Item | State |
|---|---|
| Phase 8 frontmatter | `completed` |
| Success criteria | 4/4 checked |
| plan.md table | updated (8/16 phases complete) |
| Test gate (tester) | DONE — all 15 pkgs pass `-race -count=1`; compact 75.9%, workset 94.4% coverage |
| Review gate (code-reviewer) | DONE_WITH_CONCERNS — M1 dead code fixed (deleted `buildSummaryRequest`); BRANCH prompts kept per phase spec (consumer = TUI phase) |

## Delivered

- `internal/compact/` (7 files): usage-anchored tokens (+ pure fallback post-compaction), Pi-style serialization, cut-point walk (never-toolResult, split-turn, repeat-boundary), 4 ported prompts, file-list merge, LLMSummarizer, Controller (sync persistence bridge, 3 triggers, pinned mapping, workset persistence)
- `internal/workset/` (2 files): FileStamp tracking (mtime fast path + sha256 confirm), custom-entry snapshot/restore, ContextBuilder (summary→pinned→kept→staleness; trim→compact→error budget pipeline, projection-only)
- Agent loop: 3 optional hooks (`OnMessage` sync persistence, `AfterTurn`, `OnContextOverflow` retry-once) + `Agent.Notify`
- Tools: `FileObserver` seam on read/write/edit
- Print mode: persistence moved from async event consumer to sync hook (fixes session-lag race); auto-compaction on
- Config: `compaction.reserveTokens` / `compaction.keepRecentTokens`
- Test infra: scripted `Usage` on agenttest/faux turns; 2 E2E scenarios through RunPrint

## Notable Decisions

- Anchor-staleness: post-compaction estimates switch to pure chars/4 until a fresh provider usage total arrives — prevents stale-anchor compaction loops/hard errors (found via test, fixed in `Controller.estimate`)
- Dependency direction: `workset` is a leaf (estimators injected); `compact` imports `workset`
- Pre-flight compaction rate-limited once/turn; overflow recovery bypasses that guard but is bounded by loop retry-once + `ErrNothingToCompact`

## Carry-Overs

- Phase 2 live API smoke still deferred (no `ANTHROPIC_API_KEY`) — pre-existing
- Manual `/compact [focus]` exposed via `Controller.Compact`; UI wiring lands with TUI phase
- `/pin` command (pinned flag producer) lands with TUI phase; survival path tested via entry flags

## Next

Phase 9 (Permission Engine, needs 4+5 ✅) or Phase 10 (TUI, needs 7 ✅) — both unblocked.

## Unresolved Questions

None.
