---
title: "mixi-agent: Production-Grade Go Coding Agent"
description: "Pi-inspired Go coding agent: multi-provider streaming, agent loop, JSONL sessions, compaction, MCP, permissions, subprocess extensions, Bubble Tea TUI"
status: in-progress
priority: P2
branch: "master"
tags: [go, agent, llm, greenfield]
blockedBy: []
blocks: []
created: "2026-06-03T06:42:52.410Z"
createdBy: "ck:plan"
source: skill
---

# mixi-agent: Production-Grade Go Coding Agent

## Overview

Greenfield Go implementation of a coding agent CLI, architecturally ported from the Pi coding agent (TypeScript) and hardened where Pi has gaps: MaxTurns, bounded queues, panic recovery, permission engine, MCP client, file-freshness tracking, session locking, observability. Single binary `mixi` with TUI / print / RPC / replay modes.

**Design of record:** `plans/reports/brainstorm-260603-1323-mixi-agent-go-coding-agent-architecture-report.md` (approved 260603) — section refs (§N) throughout phase files point there.
**Research backing:** `plans/reports/research-260603-1112-pi-coding-agent-go-reimplementation-deep-dive-report.md`.
**Locked decisions:** Anthropic first + OpenAI Chat Completions only (no Responses API in v1) · steering drain=`all` · Windows best-effort untested · MaxTurns default 80 · subprocess JSONL-RPC extensions · Bubble Tea TUI · usage-anchored chars/4 tokens · MCP tools/stdio only.

Note: old `internal/ai` skeleton (commit `add2cf0`) was intentionally reset; its deletion was committed with Phase 1.

## Phases

| Phase | Name | Status |
|-------|------|--------|
| 1 | [AI Core Types & Streaming](./phase-01-ai-core-types-streaming.md) | ✅ Complete (260603) |
| 2 | [Anthropic Provider](./phase-02-anthropic-provider.md) | Pending |
| 3 | [Schema Validation](./phase-03-schema-validation.md) | Pending |
| 4 | [Agent Runtime Loop](./phase-04-agent-runtime-loop.md) | Pending |
| 5 | [Built-in Tools](./phase-05-built-in-tools.md) | Pending |
| 6 | [Session Persistence](./phase-06-session-persistence.md) | Pending |
| 7 | [CLI & Print Mode E2E](./phase-07-cli-print-mode-e2e.md) | Pending |
| 8 | [Compaction & Working Set](./phase-08-compaction-working-set.md) | Pending |
| 9 | [Permission Engine](./phase-09-permission-engine.md) | Pending |
| 10 | [Interactive TUI](./phase-10-interactive-tui.md) | Pending |
| 11 | [MCP Client](./phase-11-mcp-client.md) | Pending |
| 12 | [Extension Host](./phase-12-extension-host.md) | Pending |
| 13 | [Observability & Replay](./phase-13-observability-replay.md) | Pending |
| 14 | [RPC Mode](./phase-14-rpc-mode.md) | Pending |
| 15 | [OpenAI Provider](./phase-15-openai-provider.md) | Pending |
| 16 | [Hardening & Fault Injection](./phase-16-hardening-fault-injection.md) | Pending |

## Key Dependencies

- Critical path: 1 → 2 → 4 → 7 → 10. Phase 7 = first end-to-end binary.
- 3 (schema) feeds 4+5. 6 (session) feeds 7. 8 needs 4+6+7. 9 needs 4+5. 11 needs 4+9 (+`internal/wire` created in 11, shared with 12, 14). 15 only needs 1+2 patterns. 16 last.
- External deps: `santhosh-tekuri/jsonschema`, `charmbracelet/bubbletea`+`bubbles`+`lipgloss`+`glamour`, `golang.org/x/image/draw`, `bmatcuk/doublestar`, `google/uuid`. Runtime binaries: `rg` required on PATH (no auto-download); `fd` optional (pure-Go fallback, V2).

## Conventions (all phases)

- Go 1.26; `go test -race ./...` green per phase; files <200 LOC where practical; snake_case filenames.
- No code comments referencing plan/phase numbers (rule: explain the why, not the origin).
- Conventional commits, one feat commit per phase minimum.

## Validation Log

### Verification Results (260603, session 1)
- Claims checked: 8 (referenced reports, brainstorm § refs, skeleton-deletion state, rg/fd presence, Go toolchain, dep import paths)
- Verified: 6 | Failed: 2 | Unverified: 0
- Tier: Full (16 phases; greenfield → verification targeted artifacts + environment, no legacy code to contradict)
- Failures: (1) Go toolchain absent from environment (go.mod declares 1.26.3, no compiler on PATH); (2) `fd` binary absent (`rg` present)

### Decisions (validation interview, 260603)
| # | Question | Decision | Propagated to |
|---|---|---|---|
| V1 | Go toolchain missing | Cook installs Go (official tarball → `~/.local/go`, PATH) as Phase 1 step 0 | phase-01 |
| V2 | `fd` absent vs find tool | fd preferred when present; pure-Go `filepath.WalkDir`+doublestar fallback (no .gitignore semantics, noted in tool output). Supersedes brainstorm §7 find spec + D14 wording | phase-05 |
| V3 | uuidv7 source | `github.com/google/uuid` dependency | phase-06, plan deps |
| V4 | E2E fake provider | Built-in `faux` provider, always compiled; model `faux/scripted`, script via `MIXI_FAUX_SCRIPT` env | phase-07, phase-14 |
| V5 | Unified diff impl | Internal ~150-line line-based Myers diff in `internal/perm/diff.go` | phase-09 |

### Whole-Plan Consistency Sweep (260603)
- Swept all 17 plan files after propagation for: "own 40-line", "pkg/diff", "env hook", fd-only wording, missing-toolchain assumptions — all reconciled.
- Brainstorm report remains historical design of record; V1-V5 supersede it where they conflict (find-tool fallback, uuid dep). No unresolved contradictions.
