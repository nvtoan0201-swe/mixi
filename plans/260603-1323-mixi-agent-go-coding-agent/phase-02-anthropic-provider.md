---
phase: 2
title: "Anthropic Provider"
status: completed
priority: P1
effort: "4d"
dependencies: [1]
---

# Phase 2: Anthropic Provider

## Overview
Full Anthropic Messages streaming provider: request construction with cache_control + thinking config, SSE event mapping to the §4 event contract, usage extraction, thinking/redacted replay, SDK-level retries. No official SDK — hand-rolled `net/http` (control over headers/retry).

## Context Links
- Brainstorm §2 row 2, §3 (`internal/ai/anthropic/*`), §4 (StreamOptions)
- Research §2 (exact SSE sequence, field names, stop-reason map, cache points, beta headers, gotchas 1-15)

## Requirements
- Functional: handle `message_start`, `content_block_start/delta/stop`, `message_delta`, `message_stop`, `ping`, `error`; delta types `text_delta|thinking_delta|input_json_delta|signature_delta`; block tracking by index; partial tool-arg parse via `partialjson`; stop-reason mapping (`end_turn→stop`, `max_tokens→length`, `tool_use→toolUse`, `refusal→error`, `pause_turn|stop_sequence→stop`); usage from message_start + message_delta (non-null only), Total computed.
- cache_control `{type:"ephemeral", ttl?:"1h"}` at exactly 3 points: last system block, last tool, last block of last user message.
- Thinking: `{type:"enabled", budget_tokens}` (level defaults low 2048/medium 8192/high 16384); max_tokens carve-in `min(base+budget, model.MaxOutput)`; replay thinking w/ signature, redacted as `redacted_thinking{data}`; signature-less thinking downgraded to text.
- Retries: 429/5xx/conn-reset inside provider, max `opts.MaxRetries` (default 2), honor Retry-After; ctx cancel → EventError{aborted} preserving partial content.

## Related Code Files
- Create: `internal/ai/anthropic/provider.go`, `request.go`, `convert.go`, `events.go` + tests, `testdata/*.sse` golden fixtures

## Implementation Steps
1. `provider.go`: `init(){ai.Register(&Provider{})}`; `Stream` spawns pump goroutine: build request → POST `/v1/messages` (stream:true, `anthropic-version: 2023-06-01`, `x-api-key`) → sse.Decoder loop → emit events → close channel. All failure paths emit EventError then close (never panic, never leak goroutine — test with goleak).
2. `request.go`: body builder; betas: `fine-grained-tool-streaming-2025-05-14` when tools, `interleaved-thinking-2025-05-14` when thinking; cache_control injection helper with the 3 attachment points.
3. `convert.go`: `[]ai.Message` → wire messages; tool-result pairing; image blocks; thinking replay rules; orphaned tool_use → synthetic empty tool_result (research §2 transform).
4. `events.go`: block-tracker `map[int]*partialBlock{kind, buf, partialJSON}`; finalize on content_block_stop (parse args via partialjson, strip buffer); message_delta merges stop_reason+usage.
5. Golden tests: replay captured `.sse` fixtures (text-only, thinking+signature, redacted, parallel tool calls, mid-stream error, usage-with-cache) → assert exact event sequences + final message.
6. Live smoke test behind `ANTHROPIC_API_KEY` env guard + `-tags=live`.

## Success Criteria
- [x] Golden fixtures reproduce byte-exact final AssistantMessages (usage, signatures preserved verbatim) — verified by 6 golden tests (text-only, thinking+signature, redacted, parallel tools, mid-stream error, usage-with-cache), 260604
- [ ] `Complete()` against live API returns text + correct Usage (manual smoke) — DEFERRED: no `ANTHROPIC_API_KEY` in env; `live_test.go` ready behind `-tags=live`
- [x] Abort mid-stream: partial content preserved, StopReason=aborted, no goroutine leak (goleak) — verified by `TestAbortMidStreamPreservesPartial` + goleak in TestMain, 260604
- [x] cache_control placement unit-tested at all 3 points — verified by `TestCacheControlThreePoints`; note: string→array promotion structurally N/A (`UserMessage.Content` is always `[]Content`), 260604

## Completion Notes (260604)
- `go test -race -count=1 ./...` green across all 4 packages; build + vet clean.
- Code review: all 6 acceptance criteria met (report: `plans/reports/code-review-260604-0847-phase-02-anthropic-provider-report.md`). Defensive guard added for `model.MaxOutput==0` + thinking (`request.go`).

## Risk Assessment
- SSE fixture drift vs live API → record fixtures from live during dev; pin anthropic-version.
- Signature byte-fidelity: store/replay without re-encode (json.RawMessage passthrough where possible).
