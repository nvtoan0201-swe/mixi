# mixi-agent Planning Session: Research → Brainstorm → Plan → Validate

**Date:** 2026-06-03 13:23
**Severity:** Informational
**Component:** Project-wide (planning, architecture)
**Status:** Complete

## What Happened

Four-phase compressed planning day for mixi-agent Go reimplementation of the Pi coding agent. Ran exhaustive research on Pi (~99k LOC TypeScript), synthesized architecture, hydrated 16-phase implementation plan, and validated 5 critical design decisions via stakeholder interview.

## Key Decisions

1. **Sealed Union Session Tree** — Adopt Pi's loop/compaction/session-tree designs verbatim; no MCP support (Pi has none).
2. **Subprocess JSONL-RPC + Bubble Tea TUI** — Extensions to stdio RPC for session semantics; Bubble Tea for terminal UI.
3. **Token Counting** — Hardcoded usage-anchored chars/4 (no OpenAI tokenizer binary).
4. **Tool Delivery** — MCP tools over stdio only; OpenAI Chat Completions v1 (no Responses API in v1; steering drain=all).
5. **Go Toolchain Missing** — Env has NO Go compiler despite go.mod @ 1.26.3 → Phase 1 step 0 installs it; `fd` absent → v2 Find tool uses pure-Go WalkDir fallback.

## Surprises & Lessons

- **Pi has NO MCP support.** Assumed it did; it doesn't. Retroactively justified: session reuse trumps MCP coupling.
- **Working codebase missing Go compiler.** go.mod declares 1.26.3; `go version` fails. Phase 1 now explicitly handles bootstrap.
- **Uncommitted deletions.** Old `internal/ai` skeleton (commit add2cf0) intentionally untracked — superseded by sealed-union design; deletes to be committed with Phase 1.

## Architecture Highlights

- **16 phases, critical path 1→2→4→7→10.** First runnable binary at Phase 7.
- **~52 days effort** (research-backed phase sizing).
- **MaxTurns default 80**, windows best-effort, internal ~150-line Myers diff.

## Validation Outcomes

5-question interview with stakeholder:
- ✅ V1: Cook installs Go toolchain (Phase 1 step 0).
- ✅ V2: Find tool via pure-Go WalkDir (no `fd` binary).
- ✅ V3: github.com/google/uuid for uuidv7.
- ✅ V4: Internal `faux` scripted provider (MIXI_FAUX_SCRIPT env) for E2E.
- ✅ V5: Internal ~150-line Myers diff.

All 5 decisions propagated to impacted phases (01/05/06/07/09/14) with markers; whole-plan consistency sweep clean.

## Artifacts

- Research: `plans/reports/research-260603-1112-pi-coding-agent-go-reimplementation-deep-dive-report.md`
- Brainstorm: `plans/reports/brainstorm-260603-1323-mixi-agent-go-coding-agent-architecture-report.md`
- Plan: `plans/260603-1323-mixi-agent-go-coding-agent/` (plan.md + 16 phase files, 16 session tasks)

## Next Step

→ `/ck:cook` Phase 1 (bootstrap Go toolchain, scaffold CLI entry point, initialize session tree types).

**Status:** DONE
