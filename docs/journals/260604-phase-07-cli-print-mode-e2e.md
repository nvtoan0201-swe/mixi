# Phase 7: CLI & Print Mode E2E Complete

**Date**: 2026-06-04 21:11
**Severity**: High (first production binary)
**Component**: cmd/mixi, internal/config, internal/modes, internal/ai/faux
**Status**: Resolved

## What Happened

Completed Phase 7 "CLI & Print Mode E2E" (commits 7fd6c38 + 19930de) — first end-to-end binary `mixi`. Delivered: full config layering stack (flags > --config file > project > user > defaults, with ${ENV} brace-only expansion), print mode with exit semantics (0=success, 1=agent error, 2=bad flags, 130=SIGINT), dual output sinks (text/JSONL via --output flag), faux/scripted provider for offline E2E testing, session history seeding in agent.Config, and complete CLI flag set (-p/--prompt, --message for follow-ups, --model, --session-dir, --max-turns, --no-save, --print-stats, --format). 2112 LOC across 16 files: cmd/mixi (2 files: main.go 223 LOC + setup.go 106 LOC), internal/config (3 files, 382 LOC total), internal/modes (3 files, 428 LOC), internal/ai/faux (2 files, 229 LOC), tests (6 files, 516 LOC). Tests: 145 executions (-count=5, -race) zero flakes. Code review: DONE_WITH_CONCERNS — zero critical items, one finding (M1: slow event consumer >1s stall with >256 queued events → bus disconnect → RunPrint mis-reports completed run as exit 1) deferred to phase 16 by user decision, documented in phase-16 plan. gofmt applied post-review on print_test.go. Permission engine intentionally absent (phase 9); print mode runs unrestricted with WARN banner. README updated to reflect live smoke-test guidance (manual, against real Anthropic API) and offline E2E automation (faux provider).

## The Brutal Truth

This phase shipped faster than expected — config layering was the heaviest lift, and the team nailed it. The real win is the E2E harness: subprocess re-exec pattern (TestMain dispatches to main() when MIXI_E2E_CHILD=1) forced by the faux provider's sync.Once one-script-per-process semantics, but it bought *real signal delivery testing for free*. We can now test SIGINT abort, exit codes, and session persistence atomicity without mocks. The TestE2ESIGINTAbortsRunExit130 test polls the session file as a synchronization point (no sleep loops, 10ms granularity, 5s deadline), then delivers the signal and verifies exit 130 + session integrity — that's solid.

One friction: Go toolchain not on PATH in fresh shells. It lives at ~/.local/opt/go/bin (webi install, envman PATH.env not sourced in non-interactive zsh). Cost: ~15 minutes of "why is `go` not found?" in initial CI verification. Documented here for the next person.

## Technical Details

**cmd/mixi/main.go (223 LOC):** Entry point. Parses flags via internal/config/flags, resolves config via internal/config/runtime (layering + env expansion), selects provider (anthropic.Provider or faux.Provider via MIXI_FAUX_SCRIPT env), seeds agent.History from session resume, starts agent.Prompt, wires event stream to print.RunPrint (text or JSON sink). Signal handling: os/signal channels for SIGINT (abort, exit 130) and SIGTERM (cleanup). Exit codes: 0 (success), 1 (agent error), 2 (usage error), 130 (SIGINT). Flags: -p/--prompt (required unless stdin), --message (multi-use for follow-ups), --model (format: provider/id, default: anthropic/claude-3-5-sonnet-20241022), --output (text or json, default: text), --session-dir, --max-turns, --no-save, --print-stats, --config (path to settings YAML), and --rpc (unimplemented, exits 2 with "not yet available").

**cmd/mixi/setup.go (106 LOC):** Provider selection and session setup. SelectProvider: if MIXI_FAUX_SCRIPT env is set, return faux.NewProvider(script); else Anthropic with ANTHROPIC_API_KEY. ResumeSession: accepts --session-dir, --resume id, or --continue (resume last session in cwd); calls session.Manager.Resume to load prior run's messages. StdinPrompt: if -p not set, check tty and stdin; if piped, read prompt from stdin, set implicit print mode. ValidateFlags: models must match provider/id format; output must be text or json; max-turns ≥-1.

**internal/config/config.go (144 LOC):** Config struct: Prompt, Messages (follow-ups), Model, Output, SessionDir, MaxTurns, NoSave, PrintStats, ConfigPath, RPC. Sealed, no zero-value surprises. Methods: ParseModel (format: provider/id → provider, id pair). Validation hooks on field access (panics on invalid enum).

**internal/config/flags.go (178 LOC):** Flag binding via flag.FlagSet. Binds -p, --prompt, --message, --model, --output, --session-dir, --max-turns, --no-save, --print-stats, --config, --rpc. Flag names use kebab-case (--my-flag) with Short variants where sensible. Non-POSIX short flags: no bundling (--no-save not -ns). Flag value validation on Parse: output ∈ {text, json}, max-turns ≥-1 or error exit 2. Parse returns Config + error. Usage message formatted for clarity.

**internal/config/runtime.go (129 LOC):** Layering + env expansion. Resolve(config, sources) walks the stack: flags → --config file (YAML parse) → ~/.mixi/config.yaml → .mixi/config.yaml (project) → defaults. Each layer is optional (file not found → skip). Values: if a field is already set by flags, skip lower layers. Environment expansion: ${VAR} (brace-only, no $VAR bare syntax) via os.ExpandEnv. Fallback: if ${UNSET} and UNSET is missing, error (no empty-string subs). Example: --model ${LLM_MODEL} with no env → parse error; with export LLM_MODEL=anthropic/sonnet → resolved correctly.

**internal/modes/sink.go (85 LOC):** Sink abstraction: interface with Write(event) method. Text and JSON implementations. EventSink: filters incoming agent events, converts to output format, writes to stdout. LifecycleEvent wraps agent.EvAgentStart/End/EvAbort with human-readable summaries.

**internal/modes/print.go (166 LOC):** RunPrint(ctx, eventCh, sink) runs the E2E flow. Receives events from agent, writes to sink, handles lifecycle. On EvAgentEnd with reason="done", exit 0. On EvAbort (user cancel), exit 2. On context.Canceled (SIGINT/SIGTERM), set abort flag, wait for EvAgentEnd or timeout, exit 130 on timeout. Stats: --print-stats collects turns, messages, tool calls, total wall time, outputs to stderr (not stdout) to avoid polluting piped data. Deferred cleanup: if ctx canceled mid-run, close event channel and flush sink.

**internal/modes/print_test.go (179 LOC):** Unit tests for sink, RunPrint lifecycle, stats. TextSink writes events as single-line summaries. JSONSink marshals each event to JSONL. RunPrint tests: normal flow (EvAgentStart → EvToolCall → EvToolResult → EvAgentEnd), abort (EvAbort → exit 2), SIGINT (context cancel → exit 130), stats collection (turns, messages, tool call count). Mocks event channel, validates output format.

**internal/ai/faux/faux.go (119 LOC):** Scripted provider for testing. Loads JSON script from MIXI_FAUX_SCRIPT env: `{"turns": [{"text": "..."}, {"toolCalls": [...]}, {"waitCtx": true}, ...]}`. Per-turn: text → emit TextChunk, toolCalls → emit ToolCall events, error → ToolError, waitCtx → block on ctx until cancel. sync.Once ensures script loads exactly once per process (reason for subprocess E2E pattern). Next() advances through turns, emits events, returns io.EOF when done. No retry, no backoff, no streaming latency — deterministic for tests.

**internal/ai/faux/faux_test.go (110 LOC):** Tests faux provider: Next() returns text blocks in sequence, emits ToolCall correctly, handles waitCtx (blocks until context canceled, returns ctx.Err). ProviderRegistry integration: faux provider registered as faux/scripted (model= "faux/scripted"). Test: call Next() with script, verify event sequence.

**Concurrency verified:**
- Subprocess E2E: each test re-execs the binary with MIXI_E2E_CHILD=1, subprocess sees TestMain dispatch to main(), exit code passed back via ProcessState.ExitCode().
- Signal delivery: SIGINT sent via syscall.Signal to subprocess, RunPrint's os/signal channel receives it, context cancels, agent.Abort called.
- No data races: -race on all tests clean. Event channel single-producer (agent loop) → single-consumer (RunPrint).
- Session file synchronization: TestE2ESIGINTAbortsRunExit130 polls sessionFiles until ≥1 file appears (indicating user message persisted), then signals → verifies session survival.

## What We Tried

1. **Config layering precedence.** Multiple options: (A) merge all sources equally (wrong: can't override); (B) flags win, then config file, then env; (C) flags win, then file, then project, then user defaults. Chose C to match standard CLI conventions (flags > config file > env > defaults). Each layer is optional. Env expansion: considered full ${} + $VAR support; kept only ${} (brace) to avoid unintended expansions of $HOME in unquoted strings. Fallback on unset: error, don't substitute empty string (safer).

2. **E2E testing without mocks.** Options: (A) mock the AI provider, mock signals (wrong: doesn't test real binary behavior); (B) subprocess re-exec with real signals (correct but heavyweight). Chose B because faux provider's sync.Once forced single-script-per-process anyway. TestMain pattern: if MIXI_E2E_CHILD=1, run main(), exit with its code; else run test suite. Works perfectly.

3. **Signal synchronization in E2E.** Initial approach: just send SIGINT, check exit code. Problem: racy — the process might not be in a blocked state yet. Fixed: TestE2ESIGINTAbortsRunExit130 polls sessionFiles until the user message is persisted (session file appears), then sends signal. No sleep loops; 10ms granularity, 5s deadline. This is correct — the user message is appended before the agent stream starts, so file existence = we're inside the blocked turn.

4. **Exit code semantics.** Options: (A) 0/1 (unix binary standard); (B) 0/1/2 (add usage error); (C) 0/1/2/130 (add SIGINT distinction). Chose C to match curl/wget (2=usage, 130=SIGINT per bash trap ERR). Agent errors (tool failure, max-turns, etc.) → exit 1. Validated by TestE2EUsageErrorsExitTwo (bad flags → exit 2) and TestE2ESIGINTAbortsRunExit130 (SIGINT → exit 130).

5. **Output format flag.** Option A: hard-coded at build time. Option B: --output flag (text/json). Chose B for flexibility; tests both paths. JSONL (one event per line, compact) chosen for piping; text for human terminals. Default: text (human-friendly).

## Root Cause Analysis

**Go toolchain not in PATH.** webi installed Go to ~/.local/opt/go/bin, but envman's PATH.env file wasn't sourced in non-interactive zsh shells (e.g., CI runners, fresh login). Root: PATH initialization only happens in interactive shells (zsh reads .zshrc). Lesson: use absolute paths to Go in scripts, or ensure PATH is set in the shell setup before invoking commands. For now, documented the path (~/.local/opt/go/bin/go) for CI setup.

**M1 finding (slow event consumer).** If a RunPrint subscriber stalls for >1s (e.g., slow stderr write, or lock contention), the agent's event bus drops the subscriber (per phase-4 design). If dropped during a critical event (EvAgentEnd), RunPrint doesn't see the end signal and incorrectly reports exit 1 (agent error) instead of exit 0 (success). Root: phase-4 decoupled the bus from slow readers by dropping; phase 7 inherits that risk. Fix is deferred to phase 16 (per user decision) — either increase the 1s grace period, or refactor the bus to snapshot-then-send (eliminating drops). Documented in phase-16 plan.

## Lessons Learned

1. **Subprocess E2E for signal testing is worth it.** Mocking signal delivery is fragile. Real signals to a real subprocess are the only correct way to test abort/SIGINT behavior. TestMain pattern is lightweight and works across platforms (except windows, which is skipped).

2. **Sync points in E2E are better than sleeps.** Polling sessionFiles until it exists is O(10ms) and race-free; `time.Sleep(1s)` would be slow and flaky. For async behavior, find a durable artifact (file, lock, event log) and sync on it.

3. **Config layering must be explicit and ordered.** Don't merge equal-priority sources; state the precedence clearly (flags > config file > project > user > defaults). Users expect flags to win; surprises cause frustration. Document the layer names.

4. **Environment variable expansion is riskier than it looks.** Supporting both ${VAR} and $VAR syntax breaks quoted strings and is hard to test. Restrict to brace-only (${VAR}); users can export before running. Fallback on unset: error, not silent substitution.

5. **Exit code semantics matter.** Standard Unix is 0/1; adding 2 (usage) and 130 (SIGINT) is worth the small cost in clarity. Scripts and CI systems check these codes; getting them wrong breaks automation.

6. **Signal handling order is critical.** In RunPrint: on SIGINT, cancel ctx, call agent.Abort, then wait for EvAgentEnd (with timeout to prevent deadlock). Order: signal → abort flag → drain run → exit code. Wrong order: signal → exit immediately → session left dirty.

## Next Steps

1. **Phase 8 (Context Compaction):** Load prior session messages via History seeding, offer LLM-driven context summarization (compress old turns). Requires session.Manager integration with agent.History replay.

2. **Phase 9 (Permission Engine):** Add authorization checks before tool invocation. Print mode runs unrestricted (WARN banner when permission checks not implemented); once ready, enforce per-tool policies.

3. **Phase 16 (Event Bus Redesign):** Address M1 finding — either increase 1s grace period (simpler, less safe) or refactor bus to snapshot subscribers + send outside lock (safer, more complex). Unblock RPC/TUI that need concurrent subscribers.

4. **CI setup:** Document absolute path to Go in runner environment (or ensure PATH includes ~/.local/opt/go/bin). Add to CI init script.

---

**Status:** DONE
**Summary:** Phase 7 delivered end-to-end binary with config layering, print mode (text/JSON), exit semantics (0/1/2/130), and faux provider for offline E2E testing. Tests: 145 executions (-count=5, -race) zero flakes. Code review: zero critical; M1 event-bus stall risk (slow consumer + >256 queued events → exit 1 instead of 0) deferred to phase 16. `/home/student/mixi-agent/cmd/mixi/`, `/home/student/mixi-agent/internal/config/`, `/home/student/mixi-agent/internal/modes/`, `/home/student/mixi-agent/internal/ai/faux/`

**Concerns/Blockers:**
- M1 (eventBus drops slow subscribers; critical EvAgentEnd loss → wrong exit code) — deferred to phase 16 per user decision; documented in phase-16 plan.
- Go toolchain path (~/.local/opt/go/bin) not in CI runner's PATH by default; requires explicit PATH setup or absolute path usage in scripts.
