# Phase 8: Context Compaction & Working Set

**Date**: 2026-06-04 22:40
**Severity**: High (context overflow pipeline + token estimation)
**Component**: internal/compact (7 files, 1.6k LOC), internal/workset (2 files, 0.45k LOC), agent hooks, print mode
**Status**: Resolved

## What Happened

Shipped Phase 8 "Compaction & Working Set" (commit e034661) — 3.4k insertions across 32 files. Delivered: `internal/compact/` (controller, compactor, LLM summarizer, token estimator, serialization, file operations, 4 ported summarization prompts); `internal/workset/` (workset tracking with FileStamp mtime fast-path + sha256 validation, ContextBuilder with trim→compact→error-budget pipeline); agent loop hooks (sync `OnMessage` for persistence, `AfterTurn`, `OnContextOverflow` retry-once); print mode persistence moved from async event consumer to sync hook; tools (FileObserver seam on read/write/edit); E2E test scenarios (threshold compaction via scripted usage, staleness notice). Tests: all 15 packages green under `-race`; compact 75.9% / workset 94.4% coverage. Gates: code-reviewer found and killed one dead-code helper (`buildSummaryRequest`); BRANCH-specific summarization prompts retained per phase spec (TUI phase wires the UI).

## The Brutal Truth

The architecture looked bulletproof in design review. Anchor-based token estimation (recording the last provider usage total and deriving budget deltas) survived code review intact, then two controller unit tests demolished it in minutes. The credit-ledger idea was elegant — too elegant. Post-compaction, the recorded anchor still described the pre-compaction context, so budget pipeline threw `ErrNoTokensRemaining` despite a successful compaction. The frustration: we built a whole secondary estimator just to avoid this, then discovered the primary one was unsound. The relief: the fix (switching to pure chars/4 estimation until fresh usage arrives via `anchorFresh` flag) made the system simpler, not more complex. Lesson: test early against the actual loop, not just the math.

The async-to-sync message persistence move was subtle and critical. The async event bus could lag behind the loop's turn boundary, so a compaction cut point decided "include turns 1–3" but turn 4's events hadn't been persisted yet. Loop now owns the OnMessage hook (sync, inside the request boundary), guaranteeing session state is complete before the next request. This is the kind of race that only surfaces under load or in integration tests — design review can't catch event-ordering bugs.

## Technical Details

**internal/compact/tokens.go (135 LOC):** Token estimation. `UsageAnchor` records provider's usage.input + usage.output from a compaction point. `Controller.estimate()` first tries `anchorDelta()` (current usage minus anchor), but if the anchor is stale (post-compaction, before fresh usage arrives), falls back to `charsFallback()` (chars/4 heuristic). The `anchorFresh` flag is set by `Controller.OnUsage()` when a new provider usage total arrives; it's cleared after compaction to force fallback mode. Delta-based estimation is more accurate when the anchor is fresh; fallback is always safe but less precise.

**internal/compact/compact.go (262 LOC):** Core compaction algorithm. `cut()` selects a split point (never inside a tool-result message, prefers turn boundaries, respects repeat-boundary guard). `serialize()` produces a "before" snapshot (Pi-style: placeholder → encoded metadata + truncated content). `mergeFiles()` updates the file list to remove dropped entries. `estimateCost()` projects savings. Compaction is idempotent: re-running on the same context yields the same result (verified via snapshot/restore round-trip tests). Error path: `ErrNothingToCompact` if no safe cut point exists (all messages are tool results).

**internal/compact/controller.go (374 LOC):** The integration hub. `Controller.PreFlight()` runs before each turn (once/turn, rate-limited). `Trigger()` can force compaction (overflow, explicit `/compact` command, or post-turn staleness notice). Three hooks wired into agent loop: `OnMessage` (persists entries to workset immediately, sync), `AfterTurn` (triggers post-turn compaction if staleness detected), `OnContextOverflow` (compacts then retries once). State machine: pending→compacting→done or hard-errored (switches to read-only). `Compact()` is the public facade; tests call it directly. Bug fix: after compaction completes, `anchorFresh` is cleared so `estimate()` uses fallback until fresh provider usage arrives (avoids stale-anchor false positives).

**internal/compact/serialize.go (106 LOC):** Serialization codec (Pi-style). Truncates message content (first 50 chars), encodes metadata (role, turn, ts, hash). On restore, placeholder message is re-fetched from workset. Supports custom entries (pinned messages, user-added context) with snapshot/restore hooks. Round-trip tested: serialize→deserialize produces bitwise-identical state.

**internal/compact/prompts.go (81 LOC):** Four ported summarization prompts (system, one-shot, split-turn refine, branch-continuation). Stateless templates; controller injects context (turn range, message count, file summary). Prompts are BRANCH-specific (future TUI phase supplies its own).

**internal/workset/workset.go (217 LOC):** Persistent message store. `FileStamp` tracks mtime + optional sha256. Fast path: mtime unchanged → skip re-read. Confirmation path: mtime changed → re-hash to detect stale/corrupt entries. Custom entry API for pinned messages and user-added context. Snapshot/restore hooks for compaction round-trips. Schema: JSON array of entries (role, content, ts, custom flags). Indexed access.

**internal/workset/builder.go (231 LOC):** ContextBuilder pipeline: summary (compacted messages) → pinned (user `/pin` commands) → kept (unconditional, e.g., system prompt) → staleness check. `Trim()` truncates to MaxTokens (simple greedy, newest first). `Compact()` invokes the controller. `Error()` budget overflow, initiates overflow recovery. Projection-only (doesn't mutate agent state); used by pre-flight and overflow scenarios to dry-run the budget.

**internal/agent/hooks.go (37 LOC):** Three hook types. `OnMessage(msg)` called sync inside message boundary (wired to workset persistence). `AfterTurn(outcome)` called after loop turn (staleness check). `OnContextOverflow()` called by budget code (compaction + retry-once). Hooks are optional; missing hook is a no-op.

**internal/modes/print.go:** Persistence moved from async event subscriber to sync `OnMessage` hook. Event subscriber is now read-only (observes compaction state for display). This guarantees workset state matches the loop's turn boundary.

**E2E scenarios:**
1. Threshold compaction: scripted provider usage injected via `agenttest.Usage()`. Turns increase tokens until threshold; turn N crosses the line; compaction fires; tokens reset; E2E confirms context is valid post-compaction.
2. Staleness notice: out-of-band bash append to usage file (simulates stale anchor). AfterTurn detects staleness; compaction skipped (nothing to cut); notice logged. Next turn's fresh usage clears the flag.

## What We Tried

1. **Anchor-based estimation without fallback:** Recorded usage at compaction; delta from current to anchor is the remaining budget. Problem: post-compaction, the recorded anchor still described the pre-compaction state, so delta was negative and pipeline hard-errored. Fixed by adding `anchorFresh` flag and pure-char fallback when anchor is stale. Trade-off: fallback is less precise but always safe.

2. **Async message persistence via event subscriber:** Events from the provider accumulated in an async bus, then persisted asynchronously. Problem: bus could lag behind the loop's turn boundary, so a compaction cut-point decision was made with incomplete session state. Fixed by moving persistence into sync `OnMessage` hook inside the request boundary.

3. **Compaction triggering from `OnContextOverflow` only:** Initial design treated overflow as the only trigger. Problem: staleness (anchor stale but tokens OK) was invisible. Fixed by adding `AfterTurn` check and explicit `/compact [focus]` command exposed via `Controller.Compact()`.

4. **Merging file-list without hash validation:** When dropping entries, update the file-list path. Problem: if a file was referenced multiple times, deletion was non-deterministic. Fixed by tracking actual file hashes in the file-list entry so merge is deterministic (file X with hash H is always the same artifact).

## Root Cause Analysis

**Stale-anchor hard error:** The anchor-delta estimator was sound in isolation but incomplete in context. The `anchorFresh` flag wasn't initialized when a compaction completed. Root: incomplete state machine — compaction had three outcomes (success, ErrNothingToCompact, hard error) but the state machine didn't transition `anchorFresh`. Tests caught it: two controller tests instantiated the state machine and ran compaction→estimate sequences. Fixed by explicitly clearing `anchorFresh` after compaction succeeds so estimate() uses fallback.

**Async message-persistence race:** Agent loop owns turn boundaries; persistence owned a separate async queue. If a turn completed before the event bus flushed, the compaction controller (running in the next turn) saw incomplete session state. Root: separation of concerns (loop and persistence) created an ordering race. Fixed by recognizing that persistence is part of the request boundary (must complete before external visibility), so it belongs in the loop's sync hooks, not an async bus.

## Lessons Learned

1. **Secondary estimators need fallback mode.** When delta-based estimation depends on a "fresh" state marker (anchor in this case), implement fallback logic (chars/4) so you can safely transition from one to the other. Test the transition explicitly: compaction→estimate→compaction sequence.

2. **Message persistence must be sync with the loop boundary.** Async queues are great for non-critical buffering, but persistence is part of the contract ("after this turn completes, the session state is saved"). Sync hooks owned by the loop are the right place.

3. **Tests that drive the real loop catch ordering bugs.** Unit tests of the estimator and compactor are necessary but not sufficient. E2E scenarios (scripted usage through RunPrint, out-of-band state changes) catch races that unit tests miss.

4. **Rate-limit pre-flight compaction but allow forced triggers.** Pre-flight (once/turn) prevents thrashing; explicit `/compact` and overflow recovery bypass the guard. Bounded: overflow retry-once + ErrNothingToCompact prevent infinite loops.

5. **Prompts can be phase-specific.** BRANCH summarization prompts are ported from phase spec; consumer (TUI phase) supplies its own. Keep prompts as templates in the compaction package; let consumers override. Avoids coupling.

6. **Serialization codec must support round-trip.** Verify serialize→deserialize→serialize produces bitwise-identical results. Caught one bug: timestamp encoding lost subsecond precision; fixed by using RFC3339Nano.

## Next Steps

1. **Phase 9 (Permission Engine):** Independent; unblocked. Builds on workset and file listing infrastructure.

2. **Phase 10 (TUI):** Depends on Phase 7 (already complete). Wires `/compact [focus]` and `/pin` commands, supplies TUI-specific summarization prompts, adds manual compaction UI.

3. **Live smoke test (Phase 2 deferred):** Integration test with real ANTHROPIC_API_KEY still waiting; pre-existing blocker.

---

**Status:** DONE
**Summary:** Phase 8 shipped context compaction (token estimation, cut-point selection, LLM summarization) and working-set persistence (mtime + sha256 tracking). Design review passed anchor-based estimation; unit tests found stale-anchor race and forced fallback mode. Async message persistence moved to sync hook to fix turn-boundary ordering. All 15 packages green under `-race`; compact 75.9% / workset 94.4% coverage. `/home/student/mixi-agent/internal/compact/`, `/home/student/mixi-agent/internal/workset/`, `/home/student/mixi-agent/docs/journals/260604-phase-08-compaction-working-set.md`
