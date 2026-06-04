# Phase 5: Built-in Tools Complete

**Date**: 2026-06-04 14:26
**Severity**: High (core tool interfaces + shell execution)
**Component**: internal/tools (9 tools + shared infra: truncation, accumulator, mutation queue, job control)
**Status**: Resolved

## What Happened

Completed Phase 5 "Built-in Tools" (commit 448fc36) — nine tools across ~3,300 LOC (72 tests, 81.9% coverage, -race green). Delivered: `read`, `write`, `edit`, `bash` (sync+background), `bash_output`, `kill_bash`, `grep`, `find`, `ls`. Shared infrastructure: UTF-8-safe head/tail truncation with continuation hints, rolling 2×50KiB accumulator with lazy temp-file spill (seeds spill with full pre-spill buffer so file always holds complete stream), per-realpath mutation queue for write ordering, Unix/Windows process groups with Setpgid + SIGTERM→2s grace→SIGKILL, and background job table keyed on b64 hash. Two real bugs caught during code review: (1) `Job.cursor` read in `waitForOutput` without table mutex (data race on concurrent `bash_output` calls to one job), (2) false truncation note when output exactly equals the limit (both fd-line-count and WalkDir paths over-fetched by one to detect overflow, then failed to suppress "[truncated]" when count matched limit exactly).

## The Brutal Truth

This phase was steady and methodical — no emergencies, no surprises in the design. The two bugs found by code review were real data races and silent-logic errors that would have surfaced under load or in subtle scenarios (concurrent bash_output on the same background job; bash output exactly 50KiB). The code-reviewer caught them precisely because the implementation is testable and the gates (code review + concurrency testing) work. No drama here; this is what proper gates look like in action.

The friction point is environment-specific: six tests skip because `rg` and `fd` binaries are not available locally. This is intentional (no auto-download per product decision), but it means the integration paths (exec'ing the real grep/find implementations) are untested in this session. The workaround is sound (pure-Go fallbacks + fake `lookPath` for error paths), but it's a reminder that tool integration is environment-fragile.

## Technical Details

**internal/tools/truncate.go (45 LOC):** Head and tail truncation. `headTruncate` cuts at MaxLines (2000), appends `[… N more lines]` if excess. `tailTruncate` keeps the end, prepends `[… N more lines]` if truncated. Both UTF-8-boundary-safe: scan backward to the last valid rune boundary, don't split multibyte sequences. Used by `read` (head), `bash`/`bash_output` (tail).

**internal/tools/accumulator.go (142 LOC):** Collects interleaved stdout+stderr from shell commands. Memory holds at most rolling tail (2×MaxBytes = 100KiB). Once total exceeds that, full stream lazily spilled to temp file, and memory keeps only the tail. Safe for concurrent writers (the two pipe copiers). Key insight: `spillLocked()` seeds the file with the entire buffer so far, so the file always holds the complete stream from byte 0. `readFrom(offset)` serves pre-spill deltas from memory, post-spill from file. `persistFull()` materializes the file on demand for the "[full output: path]" hint without requiring a prior spill (used when tail-truncation needs to prove full output exists). All accesses guarded by `mu`.

**internal/tools/mutqueue.go (52 LOC):** `map[realpath]*sync.Mutex` with lazy creation and per-path serialization. Used by `write` to order mutations on the same file and allow parallel writes to different files. `Lock(path)` acquires the per-path mutex; `Cleanup()` releases all. Prevents `write("/tmp/a")` and `write("/tmp/b")` from racing, while allowing both to run in parallel when pointed at different realpath targets.

**internal/tools/edit.go + editmatch.go (205 LOC combined):** Edit tool with two-pass fuzzy matching. Pass 1: exact-substring match for all `oldText` values. If any fail, pass 2: normalize both file content and needles (NFKC + typography table: smart quotes → ASCII, en/em dashes → hyphen, NBSP → space, trailing-line-ws stripped), re-match against normalized form, rewrite entire file from normalized content (the "Pi contract": whole-file normalized rewrite when fuzzy is needed — documented in tool description itself as this semantics is surprising). Uniqueness enforced: duplicate or zero matches produce explicit errors. Overlaps rejected: "merge them into one edit" message. `closestLineHint()` uses Sørensen–Dice bigram similarity to suggest the most-alike line on no-match, helping the LLM fix near-miss oldText quickly. Applied edits in reverse-offset order to keep spans valid. CRLF and BOM preserved. Returns diff, unified patch, and firstChangedLine in details.

**internal/tools/bash.go + bash_bg.go + procgroup_unix.go (240 LOC combined):** Synchronous bash execution with output accumulation, timeout, and abort. `setProcGroup` uses `syscall.SysProcAttr{Setpgid: true}` to place child in its own process group. `terminateGroup` sends SIGTERM to the group, waits for graceful exit for 2s, then SIGKILL if still alive (all via the `exited` channel so no signal is sent to a reused pgid). ExecSequential: one bash call per batch. Background mode (`background=true`) returns a job ID and registers with the job table; `bash_output` reads new bytes from a job's accumulator cursor; `kill_bash` terminates and returns final output. Updates channel receives throttled snapshots every 100ms (BashUpdateThrottle). Context abort routes to `terminateGroup` with the `exited` signal (guards against signaling after process exit). **Bug caught:** `Job.cursor` read in `waitForOutput` lacked the table mutex; concurrent `bash_output` calls on one job could race the cursor. Fixed by locking the read.

**internal/tools/grep.go (65 LOC):** Wraps `rg` (ripgrep) with JSON-lines output (`rg --json`), parses matches, enforces line-length limits (GrepMaxLineLen=500). Missing `rg` → error with install hint (no auto-download). Single-char queries rejected. Max 10,000 results.

**internal/tools/find.go (105 LOC):** Prefers `fd` when available (faster, respects .gitignore), falls back to pure-Go `filepath.WalkDir` + doublestar glob matching. `fd` path: calls `fd --type f --json` with pattern, parses JSON. `WalkDir` fallback: walks directory tree, matches paths via doublestar, enforces max 10,000 results, adds note `[fd not found: .gitignore not respected]`. Prevents runaway directory traversals. Missing `fd` is recoverable; missing `find` command itself is not (not used — purely Go or fd).

**internal/tools/read.go (70 LOC):** Reads file at 1-indexed offset (line + column) with max-lines limit. Image sniff (JPEG, PNG, GIF, WebP via stdlib + `golang.org/x/image`) + resize to ≤2000² pixels. Head-truncates to MaxLines, appends continuation hint. Oversized single lines (>500KiB) rejected with error (malformed file or binary).

**internal/tools/write.go (50 LOC):** Creates directories via mkdir-p, writes to temp file, atomically renames. Mutation queue ensures same-file writes are serialized. Optional fsync for durability. Errors on permission denied or disk full.

**internal/tools/ls.go (35 LOC):** Pure-Go listing via `filepath.Walk`. Includes file size, mod time, perm. Sorted. Max 50,000 entries.

**internal/tools/bintools.go (28 LOC):** Shared `lookPath` and `parseRgJSON` utilities. `lookPath` returns the absolute path to a binary on PATH or an error (used by grep/find to hint "install rg" or fall back to WalkDir); faking `lookPath` in tests allows error-path testing without system binaries. `parseRgJSON` deserializes `rg --json` lines.

**Concurrency verified:**
- Accumulator: two concurrent pipe copiers write to one accumulator safely (guarded by mu).
- Mutation queue: same-file writes serialize; different-file writes run in parallel.
- Job table: `bash_output` and `kill_bash` safely concurrent on different jobs; same-job concurrent calls now safe after mutex fix.
- No data races under `-race` (parallel tool dispatch within agents, accumulator and job-table access).
- No goroutine leaks: background bash jobs' done channels always closed; pipe goroutines complete before accumulator exit.

## What We Tried

1. **Accumulator spill without seeding the file:** Initial design created the temp file, then wrote new bytes to it. Problem: bytes already accumulated before spill were lost. Fixed by `spillLocked()` seeding the file with the entire pre-spill buffer. The contract is now: file always holds the complete stream from byte 0.

2. **Truncation flag without over-fetch logic:** Initial implementation used the exact line count to decide truncation, so a 2000-line output would show as NOT truncated. Problem: user sees "2000 lines" but no "[full output]" hint when the tool's actual limit is 2000. Fixed by over-fetching by one line in both fd and WalkDir paths to detect if the limit was exactly hit or exceeded.

3. **Job.cursor guarded only at the read-side:** Initial implementation acquired the mutex only when writing the cursor, not reading it. Problem: `waitForOutput` read the cursor unlocked, racing concurrent `bash_output` calls. Fixed by locking the read in `waitForOutput` (line 75-77 of bash_bg.go).

4. **Testing with real rg/fd binaries:** Attempted manual setup; deferred because auto-install denied by policy. Pure-Go fallback for `find` and fake `lookPath` in tests provide sufficient coverage for error paths. Integration tests skip cleanly in no-binary environments.

## Root Cause Analysis

**Data race on Job.cursor:** The accumulator cursor tracks the byte offset of the last read from a background job's output. Multiple `bash_output` calls can request new bytes simultaneously. The initial design read the cursor in `waitForOutput` without holding the mutex, which is used elsewhere for cursor updates. Root: copy-paste error; the adjacent lines were guarded but that specific read was overlooked. Caught by code review + concurrency testing.

**False truncation at exactly-limit output:** The truncation logic checked `if len(lines) > MaxLines`, but if the file had exactly MaxLines lines, the condition was false and no "[truncated]" note appeared. However, the display was still truncated (only showing MaxLines, not the full file). Root: incomplete logic — the truncation check should detect "we stopped early," not just "we exceeded the limit." Fixed by over-fetching by one line so that exactly-limit outputs show the (N+1)th line as proof that truncation did occur, or no proof if the file is smaller.

**Test environment friction with rg/fd:** The project's product decision is no auto-download of tools. Tests that invoke `rg` or `fd` skip if the binaries are not on PATH. The `lookPath` function is faked in tests to cover error paths (binary not found). Root cause: tools like grep and find are system-provided utilities; tests can't assume they're installed. Mitigation is sound: integration tests skip with a skip reason, unit tests use fake lookPath, pure-Go fallback (WalkDir) is tested independently.

**BOM/NBSP literal characters in source code:** Early implementation had actual BOM (U+FEFF) and NBSP (U+00A0) characters in the Go source file and JSON corpus, which caused compile errors ("illegal byte order mark"). Root: copy-paste from brainstorm/research docs that included these unicode characters as examples. Fixed by replacing with visible escape sequences in source (e.g., `﻿` → comment noting the character, or literal escape in JSON strings). Prevents invisible-character fragility in code reviews.

## Lessons Learned

1. **Accumulator spill design: seed the file with everything buffered.** Rolling-tail buffers that spill to disk need the file to represent the complete stream from byte 0, not just the new bytes written post-spill. Otherwise, readFrom() must juggle "bytes from memory + bytes from file" logic. Simpler contract: file always has the complete stream, memory always has the complete stream until tail-truncation is needed.

2. **Cursor synchronization in concurrent read/write scenarios.** When multiple goroutines access a mutable field (job.cursor), lock every access, not just every write. Read-side races are just as real.

3. **Over-fetch to detect boundaries.** When your truncation logic is "stop at MaxLines," you can't distinguish "we stopped exactly at MaxLines" from "there is no (MaxLines+1)th item." Over-fetch by one to make the distinction: if the (N+1)th item exists, you truncated; if it doesn't, you have the complete output.

4. **Surprising semantics need documentation in the tool description itself.** The two-pass edit fuzzy-match (whole-file rewrite when any edit needs fuzzy) is non-obvious. Don't rely on a buried comment — state it in the tool's Description() method so the LLM reads it every time.

5. **Fake lookPath beats skipping tests.** Rather than skip the "binary not found" error path for grep/find, fake the lookPath function to return a "not found" error and test the error-message generation logic. Allows full coverage even in locked environments.

6. **Process-group termination needs careful signal ordering.** SIGTERM → 2s grace → SIGKILL is the right sequence for shell subprocesses. The grace window must be observable: watch the `exited` channel to know when the group actually exited, so the SIGKILL is only sent if needed. A fixed 2s delay without checking could kill a process that already exited, potentially racing a PID reuse.

7. **Literal Unicode edge cases in tests.** If your test corpus or source code includes literal BOM, NBSP, or smart quotes, the Go compiler or file-encoding tools will silently mangle them or cause compile failures. Use escape sequences; test that the normalization table handles them correctly via escaped input.

## Next Steps

1. **Phase 6 (Session Persistence):** Independent of tools; can run in parallel. Requires saving agent run state (pending messages, partial streams, tool results) to storage.

2. **Phase 7 (Agent Shutdown):** Will wire `JobTable.KillAll()` into the agent's cleanup path so background bash jobs don't outlive the process.

3. **Phase 8 (Context Compaction):** Will use edit, read, and write tools for context management; LLM-driven summarization of past messages.

4. **Live tool integration testing:** Manual validation of `rg --json` and `fd --json` parsing when binaries are available. Not a blocker; golden test cases cover the wire format.

---

**Status:** DONE
**Summary:** Phase 5 delivered 9 tools across 3,300 LOC with shared infrastructure (accumulator, mutation queue, process groups, job control). Code review found 2 real bugs (cursor data race, false truncation at exactly-limit) — both fixed same session. Tests: 72 pass / 6 env-skips (no rg/fd locally), -race green, 81.9% coverage. `/home/student/mixi-agent/internal/tools/`
