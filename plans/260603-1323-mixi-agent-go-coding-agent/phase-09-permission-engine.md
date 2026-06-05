---
phase: 9
title: "Permission Engine"
status: completed
priority: P1
effort: "3d"
dependencies: [4, 5]
---

# Phase 9: Permission Engine

## Overview
`internal/perm`: first-class permission engine — 4 modes (plan/prompt/auto-edit/yolo), allow/deny rule globs, dangerous-pattern screen, diff preview generation, Asker interface, session grants. Wired into the loop's tool-call path ahead of extension filters. Removes phase 7's yolo banner.

## Context Links
- Brainstorm §12 (decision pipeline, config format, rule syntax, baseline restrictions, grants — implement exactly), §5 #3 (Asker)
- Research §11 (Pi's absence; example permission-gate semantics)

## Requirements
- `Decide(call)`: category map (read|write|execute|mcp) → explicit rules most-specific-first (deny beats allow; project deny appends to global) → session grants → mode defaults → ASK via Asker.
- Rule syntax: `bash(prefix*)` command glob; `read/write/edit(glob)` doublestar on cleaned abs path (relative anchored at cwd); `mcp__server__tool` exact/glob; `--allow/--deny` flags highest precedence.
- Baselines (non-yolo): write/edit outside cwd subtree → forced ASK; secret-glob reads (`**/.env*`, `**/*_rsa`, `**/credentials*`) → forced ASK; `denyPatterns` regex screen for bash.
- Preview: edit → dry-run unified diff (reuse editmatch without applying); write → diff vs existing or "new file, N bytes"; bash → command+cwd; mcp → pretty args.
- Asker impls this phase: headlessAsker (deny w/ reason unless explicit yolo/auto-edit). TUI/RPC askers land in phases 10/14.
- Grants: in-memory per session; generalization rules (bash first-token+`*`; file ops dir+`/**`); `/permissions` listing data structure (UI later).
- SandboxSpec field returned but unused (v2 seam, D-§12).

## Related Code Files
- Create: `internal/perm/policy.go`, `engine.go`, `ask.go`, `diff.go` + tests
- Modify: `internal/agent/loop.go` (filter slot before BeforeToolCall hook), `internal/config/config.go` (permissions block), `internal/modes/print.go` (headless asker, remove banner)

## Implementation Steps
1. `policy.go`: config structs + rule parser (`tool(spec)` grammar) + doublestar dep; parse-error reporting w/ file+rule context.
2. `engine.go`: decision pipeline; category table; grant store + generalizer; exhaustive table tests (mode × category × rule × grant matrix).
3. `diff.go`: internal line-based Myers diff (~150 LOC) emitting unified format; edit dry-run reuse. <!-- Updated: Validation Session 1 - V5 -->
4. `ask.go`: AskRequest{Tool, Args, Preview, Reply chan}; headless impl.
5. Loop wiring as EventFilter (§5 #8) — denial → IsError result `Permission denied: <reason>` (LLM-visible).
6. E2E: print mode with deny rule blocks write; `--permission-mode yolo` bypasses; secret-glob read forced-ask→denied headless.

## Success Criteria
- [x] Decision matrix test: every mode × category default verified
- [x] deny beats allow; project deny cannot be weakened by global allow (test)
- [x] Edit preview diff equals post-execution diff for same args (consistency test)
- [x] Headless: ASK→deny with actionable reason text; explicit modes loosen as specced

## Completion Notes (260605)
- Filter seam: `agent.ToolCallFilter` interface in `internal/agent/hooks.go` (not a premature `ext` package); cmd adapter in `cmd/mixi/permissions.go`. Phase 12's extension host implements the same interface.
- Bash grant generalization: first TWO tokens + `*` (`go test ./...` → `bash(go test*)`), per brainstorm §12 example — supersedes this file's "first-token" wording (user decision, validation 260605).
- Secret-glob forced-ASK extended to `grep` targeting a secret path (review finding: grep returns contents like read). Directory-grep over a tree containing secrets is NOT screened — output-level filtering deferred to v2.
- Preview/execution TOCTOU window accepted for v1 (policy-not-kernel scope, user decision 260605); v2 hardening candidate alongside SandboxSpec.
- Behavior change: headless default (prompt) mode now denies write/execute/mcp with actionable reason; e2e milestone test updated to pass `--permission-mode auto-edit`.
- Tests: perm pkg 90.9% coverage; full repo `go test -race` green; E2E deny/yolo/secret scenarios in `cmd/mixi/e2e_permissions_test.go`.

## Risk Assessment
- Rule grammar scope creep → freeze grammar to brainstorm §12; extensions can layer custom gates later.
- Path-cleaning edge cases (symlinks out of cwd) → resolve realpath before subtree check; test symlink escape.
