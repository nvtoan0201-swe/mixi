---
phase: 8
title: "Compaction & Working Set"
status: completed
priority: P1
effort: "4d"
dependencies: [4, 6, 7]
---

# Phase 8: Compaction & Working Set

## Overview
`internal/compact` + `internal/workset`: usage-anchored token estimation, cut-point selection, Pi-verbatim summarization prompts, split-turn handling, WorkingSet file-staleness tracking, ContextBuilder with budget pipeline (trim → compact → error) and pinned-entry survival. Closes Pi's context-management gap.

## Context Links
- Brainstorm §8 (WorkingSet/ContextBuilder spec — implement exactly), §10 (compaction algorithm 11 steps, trigger conditions)
- Research §5 (Pi has neither — what it does instead), §7 (Pi compaction internals, prompts to port)

## Requirements
- Tokens: `estimate = lastAssistantUsage.Total + Σ ceil(chars/4)` for trailing; pure heuristic when no anchor.
- Triggers: post-turn auto (`estimate > window − reserve(16384)`), pre-flight budget, overflow recovery (wire the phase-4 stub), manual `/compact [focus]`; `compaction.enabled=false` kill-switch.
- Cut-point: backward walk accumulating ≥ keepRecent(20000); never cut at toolResult; extend over non-message entries; split-turn → TURN_PREFIX second summary merged with `---`; window starts at previous compaction's firstKeptEntryId.
- Prompts: SUMMARIZATION / UPDATE_SUMMARIZATION / TURN_PREFIX / BRANCH ported verbatim from research §7 + pinned-exclusion sentence; serialization `[User]:`/`[Assistant]:`/2000-char tool-result clip.
- WorkingSet: FileStamp{readTurn, writeTurn, mtime, size, sha256}; RecordRead/RecordWrite from tools (phase-5 tools gain workset calls here); Stale() check; persisted as `custom{customType:"workingset"}` entries; restored on resume.
- ContextBuilder order: sysprompt → compaction summary → pinned → kept history → staleness notice (cap 20 files) → (steering by loop). Budget: trim old fat tool results (>1KiB, older than last 2 turns, largest first, build-time only) → compact → hard error.
- Summarizer interface (§5 #7); failure → abort compaction, log, trim-only fallback; entry appended only on success.

## Related Code Files
- Create: `internal/compact/compact.go`, `prompts.go`, `tokens.go`, `serialize.go`, `fileops.go`, `internal/workset/workset.go`, `builder.go` + tests
- Modify: `internal/tools/read.go`, `write.go`, `edit.go` (workset recording), `internal/agent/loop.go` (overflow path wiring), `internal/modes/print.go` (auto-compaction on)

## Implementation Steps
1. `tokens.go` + table tests (anchor present/absent, role coverage).
2. `serialize.go` + golden snapshot.
3. `compact.go`: cut-point algorithm w/ fixture sessions covering: clean cut, toolResult skip, split turn, repeat compaction boundary.
4. `prompts.go`: constants; pinned-exclusion appended when pinned entries exist.
5. `workset.go`: stamps, Stale() (mtime fast path, hash confirm), persistence round-trip.
6. `builder.go`: assembly + budget pipeline; trim is projection-only (session untouched) — assert session bytes unchanged.
7. Wire into loop: post-turn trigger, pre-flight check, overflow recovery (drop failed msg → compact once → retry once).
8. E2E with fake provider: long scripted session crosses threshold → compaction entry appended, next request contains summary + kept tail; staleness notice appears after out-of-band file touch.

## Success Criteria
- [x] Cut-point fixtures match expected firstKeptEntryId in all 4 scenarios
- [x] Compaction failure leaves session byte-identical (no partial entry)
- [x] Stale file modified externally → notice in next context build; re-read clears it
- [x] Pinned message survives compaction and appears post-summary in built context

## Risk Assessment
- chars/4 drift on code-heavy content → reserve absorbs by design (D9); add debug log of estimate-vs-actual each turn for tuning.
- Trim+compact interaction loops → trim only runs pre-flight, compact max once per turn; assert no recursion.
