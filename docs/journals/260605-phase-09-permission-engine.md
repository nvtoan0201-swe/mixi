# Phase 9: Permission Engine

**Date**: 2026-06-05 10:15
**Severity**: High (first-class access control on every tool call)
**Component**: internal/perm (12 files, 1.5k LOC), internal/agent (ToolCallFilter interface), cmd/mixi (wiring + E2E)
**Status**: Resolved

## What Happened

Shipped Phase 9 "Permission Engine" (commits 363abbe + c2c3bd8) — gate every tool call with mode-based decision pipeline (4 modes: plan/prompt/auto-edit/yolo), explicit allow/deny rules, forced-ASK baselines for escapes and secrets, diff previews on write/edit, in-memory session grants with generalization. Wired as `agent.ToolCallFilter` ahead of the BeforeToolCall hook; fails closed on filter error. Tests: all 14 packages green under `-race`; perm 90.9% coverage. Code review (code-reviewer) flagged secret-glob screen was tool-specific (`read` only), not category-wide — `grep` can exfiltrate secret file contents. User decision: extend to `grep`, document directory-grep leak as v2 scope. Behavior change: headless default (prompt) mode now denies write/execute/mcp; E2E milestone test updated with `--permission-mode auto-edit`.

## The Brutal Truth

The permission system felt like it needed to be airtight, and the spec made it look airtight. The decision pipeline itself was sound — deny beats allow, baselines can't be pre-approved, mode defaults are clear. But code review caught a category-vs-tool-name gap that nearly slipped past into production: the secret-glob screen was hard-coded to check `info.Tool == "read"`, which only blocks the `read` tool, not every tool in the read category. A user could run `grep -r API_KEY .env` or `grep . credentials.json` and dump secrets into the transcript, completely bypassing the screen. The spec said "read of files…" (literal), but we built a screen designed to *block secret exfiltration*, not to achieve spec-literal compliance at the cost of a real leak. This is exactly the kind of gap that doesn't show up in happy-path tests — the decision matrix tests pass because they test decision outcomes, not threat modeling. The frustration: we were three hours from shipping this, and code review's threat-model lens caught what unit tests missed.

The other issue was architectural friction around where the filter interface lives. The first instinct was to put it in a future `ext` package (extensions), but that felt premature — only one consumer exists (the permission engine adapter in cmd/mixi), and a future extension host (phase 12) will implement the same interface. Putting it in a nearly-empty package would be premature separation. Putting it directly in `internal/agent` felt like coupling, but the reality is the filter slot *is* part of the agent loop's public contract (same way hooks are). The decision: define ToolCallFilter in `internal/agent/hooks.go`, implement the permission adapter in `cmd/mixi/permissions.go`. Clean, no empty intermediate package. This is a lesson about when to separate: don't split to separate concerns, split when you have multiple distinct implementations with pressure to keep them independent.

The third gut-punch was bash grant generalization. The brainstorm spec said "first token" as the grant prefix, but the example was `go test ./...`, which should widen to `bash(go test*)`, not `bash(go*)`. That means two tokens, not one. Three hours of debate with the user before clarifying: the spec said one thing, the example showed two, and the safer interpretation is the example (fewer false denials). Locked in: first TWO tokens + asterisk. This teaches a lesson: when spec and example conflict, trust the example — it's usually what the user actually wanted.

## Technical Details

**internal/perm/engine.go (374 LOC):** Core decision pipeline. `Decide(call)` evaluates in order: flag rules (--deny/--allow override; set via `applyCLIFlags`) → settings deny → non-yolo baselines (denyPatterns regex for bash commands; outside-cwd write/edit; secret-glob read) → settings allow → session grants → mode defaults → ask via Asker. Each step returns early if decision is reached. Category mapping (bash→execute, read→read, write→write, edit→write, mcp→mcp) is exhaustive; unknown tools default to execute (most restrictive). `Ask(call)` drives the Asker, records the decision as a generalized grant, and returns the result. `Result` includes the denial reason (actionable: "Permission denied: outside repository, use --permission-mode yolo", etc.) and a SandboxSpec seam (unused v1, hooks for v2 kernel-level isolation).

**internal/perm/policy.go (188 LOC):** Config structs (Settings.Allow, Settings.Deny with rule lists) and rule parser. Rule syntax (frozen to brainstorm §12): `bash(prefix*)`, `read/write/edit(globpath)`, `mcp__server__tool(exact|glob)`. Path globs use doublestar (relative anchored at cwd; absolute treated as paths). Parser validates on construction; ParseError includes file+line context. DenyPatterns is a regex list for command pattern screening (e.g., `.*sudo.*`, `.*rm.*`). Each tool category maps to rules via explicit switch.

**internal/perm/grants.go (118 LOC):** In-memory grant store with mutex-protected add/match/list. Grants record "user said yes to X" and widen on next invocation (bash: first two tokens + `*`; file ops: containing directory + `/**`). Generalization is conservative: `bash(go test ./cmd/mixi/...)` widens to `bash(go test*)`, which covers `go test ./...`, `go test ./cmd/...`, etc. File grants: `/path/to/file` widens to `/path/to/**`, so approving one file approves the containing directory. No persistence (in-memory only, per-session); scope is intentional to prevent "sneak a grant in once, it sticks forever."

**internal/perm/diff.go (176 LOC):** Line-based Myers diff (~150 LOC) outputting unified format (reuses exact same pipeline as `edit` tool's matchEdits/validateOverlaps/applyEdits). `UnifiedDiff(oldText, newText)` computes the diff and formats it for human review. E2E test (TestEditPreviewMatchesExecution) verifies that preview diff == post-execution diff — approval UI shows the exact reality of what the edit will produce.

**internal/perm/preview.go (86 LOC):** Tool-specific preview generation. Edit → unified diff (via DryRunEdit); write → diff vs existing or "new file, N bytes"; bash → command echo + cwd; mcp → pretty-printed args. Previews are read-heavy (file exists checks, content reads for diff), creating a TOCTOU window between preview and execution. Accepted for v1 per policy-not-kernel scope; v2 candidate for hardening via SandboxSpec.

**internal/perm/ask.go (38 LOC):** AskRequest struct (Tool, Args, Preview, Result chan). HeadlessAsker (cmd/mixi) returns deny on unapproved calls; reason includes the rule that would have allowed it (e.g., "use --allow 'read(/path/to/file)'" or "use --permission-mode yolo"). TUI/RPC askers land in phases 10/14.

**internal/agent/hooks.go (ToolCallFilter interface added):** Sits on the tool-call path ahead of BeforeToolCall; one pipeline, two members: permission engine (this phase) and extension host (phase 12). Signature: `FilterToolCall(ctx, call) (args json.RawMessage, block *BlockDecision, err error)`. Block reason is sent to model verbatim (no post-processing).

**internal/tools/edit_dryrun.go (55 LOC):** Exported `DryRunEdit(oldText, editArgs, oldPath)` reuses the identical match/validate/apply pipeline as `edit.Execute`. Exported so preview can reuse the exact seam without duplicating match logic — consistency is guaranteed by construction.

**cmd/mixi/permissions.go (142 LOC):** Adapter wiring. `NewPermissionFilter(policy, asker)` returns a `ToolCallFilter` impl. `ApplyCLIFlags(policy, flags)` applies --allow/--deny to override config. HeadlessAsker denies unapproved calls (unless explicitly yolo or auto-edit mode).

**E2E scenarios:**
1. Deny rule blocks write (prompt mode): `mixi --deny 'write(main.go)'` + write attempt → Permission denied, stderr explains the block.
2. Grant generalization: approve `read(/path/file)` once → next `read(/path/file)` passes without asking; `read(/path/other)` asks (different file, same dir would be OK if grant had been dir-wide).
3. Secret glob forced-ask: `read(.env)` in prompt mode → forced ask even if allow rule exists.
4. Yolo bypasses all: `--permission-mode yolo` → no asks, all categories allowed.

## What We Tried

1. **Secret-glob tool-specific check:** Checked `info.Tool == "read"` to force-ask secret file reads. Problem: `grep`, `find`, `ls` in the read category could exfiltrate the same secrets. Fixed by user decision: extend forced-ask to `grep` targeting secret paths. Directory-grep leak (recursive over a tree containing secrets) documented as v2 output-level filtering.

2. **Filter interface in future `ext` package:** Felt cleaner to separate future extensions. Problem: only one consumer exists (permission engine), and the extension host isn't built yet. Premature separation with no win. Fixed by defining ToolCallFilter in `internal/agent/hooks.go` alongside other contracts. Extension host implements the same interface when it lands in phase 12.

3. **Bash grant "first token only":** Matched the spec word-for-word. Problem: example was `go test ./...` → `bash(go test*)` (two tokens), not `bash(go*)` (one token). False positive denials on the example case. Fixed by user clarification: spec said one, example showed two, trust the example. Bash grants: first TWO tokens + asterisk.

4. **Deny beats allow via flag ordering:** Evaluated flag rules first. Problem: if both --allow and --deny apply to the same call, need to ensure deny wins. Fixed by: (a) flag eval precedes settings (flags always win), (b) deny check precedes allow at each layer (deny wins at tie).

## Root Cause Analysis

**Secret-glob tool-specific gap:** We optimized for the happy path (blocking `read` of secrets) without running a threat model on the entire read *category*. Code review's angle was simple: "what other tools can print file contents?" That question immediately surfaced grep/find/ls as vectors. Root: we tested decision outcomes (decision matrix), not threat scenarios (can this secret leak?). The spec was ambiguous (tool-literal vs. category-literal), and we picked the letter rather than the spirit.

**Premature ext package:** We were pattern-matching to "separate concerns" without verifying that we had multiple distinct concerns yet. Root: confusing "reusable interface" (ToolCallFilter is reused by future extensions) with "separate package" (doesn't need its own package until pressure from multiple independent implementations exists). The fix was philosophical: interfaces live where the first consumer lives, or in the shared public contract (hooks.go).

**Bash grant one-token vs. two-token:** The spec and example diverged. Spec is the source of truth until it conflicts with a real use case. Example is a real use case. Root: spec didn't have a worked example when written; brainstorm added one later. When they collide, the example usually reflects what was actually intended.

## Lessons Learned

1. **Threat modeling beats happy-path testing.** Unit tests of decision outcomes are necessary but not sufficient. Code review's "can the attacker do X?" caught what the decision matrix missed. Generalize: when building a security primitive, pair unit tests with adversarial scenarios (what happens if I try to exfiltrate this?).

2. **Exporting a dry-run seam beats duplicating logic.** DryRunEdit is ~55 LOC exported from `internal/tools/edit.go`. We could have duplicated the match/validate/apply logic in the perm package's preview module. Exporting the seam means preview and execution are the same, verified by a single round-trip test. Lesson: when you find yourself duplicating a complex routine (diffs, parsing, matching), extract and export the seam instead.

3. **Define interfaces where the first consumer lives.** ToolCallFilter could have gone in `internal/agent/hooks.go` (where it's used) or an empty `internal/ext` package (where future extensions will land). Putting it in hooks.go added no coupling (hooks already define agent public contracts) and avoided an empty intermediate package. Generalize: don't split to separate concerns; split when you have multiple concurrent implementations under independent change pressure.

4. **Reviewer threat modeling is force-multiplier.** Code review's question ("what other tools can print file contents?") was one sentence and surfaced a real category-vs.-tool gap that unit tests didn't catch. Running threat scenarios is worth the time.

5. **Spec and example divergences are tie-breakers toward the example.** Specs are aspirational; examples are what the user actually wants. When they conflict, the example usually reflects the real requirement.

6. **Baselines cannot be pre-approved by allows.** The architecture explicitly prevents allow rules from pre-approving forced-ask baselines (outside-cwd write, secret reads). This is correct: a baseline is a baseline. Test it: TestWriteOutsideCwdForcesAsk verifies that even with `--allow 'write(/anywhere)'`, escapes still ask.

7. **Session grants are intentionally ephemeral.** In-memory only, no persistence. This trades UX friction (re-approve on next session) for a hard cap on permission creep. Correct for v1; v2 can add durable session grants with explicit user confirmation.

## Next Steps

1. **v2 Hardening:** SandboxSpec seam (Result.Sandbox always nil in v1) allows future phases to add kernel-level isolation (seccomp, pledge, pledge-style capabilities). TOCTOU window (preview reads file, execution re-reads under mutex) acceptable for v1; v2 candidate.

2. **Secret-glob extension to grep:** User approved extending forced-ask to `grep` targeting secret paths (code-reviewer finding M1). Directory-grep leak (recursive over tree containing secrets) documented as output-level filtering deferred to v2.

3. **Phase 10 (TUI):** Depends on Phase 7 (CLI + print mode). Wires `/permissions` listing data structure, supplies TUI-specific Asker impl, adds manual permission UI.

4. **Phase 12 (Extension Host):** Implements same `ToolCallFilter` interface. Chains after permission engine in filter pipeline.

---

**Status:** DONE
**Summary:** Phase 9 shipped permission engine gating every tool call (4 modes, rule pipeline, forced-ASK baselines, diff previews, session grants). Code review's threat-model lens found secret-glob was tool-specific not category-wide; user approved extend to grep; directory-grep leak → v2. Architectural decisions: ToolCallFilter in agent/hooks.go (first consumer), DryRunEdit exported (reuse not duplicate), bash grants first-TWO-tokens per example. All 14 packages green under `-race`; perm 90.9% coverage. `/home/student/mixi-agent/internal/perm/`, `/home/student/mixi-agent/docs/journals/260605-phase-09-permission-engine.md`
