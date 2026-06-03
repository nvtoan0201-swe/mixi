---
phase: 1
title: "AI Core Types & Streaming"
status: completed
priority: P1
effort: "3d"
dependencies: []
---

# Phase 1: AI Core Types & Streaming

## Overview
Foundation package `internal/ai`: sealed type unions (Content, Message, StreamEvent), Usage/Model/Context types, provider registry, SSE decoder, partial-JSON repair. Zero internal deps. Supersedes deleted skeleton from commit `add2cf0` — commit that deletion here.

## Context Links
- Brainstorm §4 (complete type system — copy verbatim), §5 #1 (Provider interface), §2/§3 (package layout)
- Research §2 (normalized types, event pipeline, partial-JSON algorithm, SSE quirks)

## Requirements
- Functional: sealed unions w/ marker methods; JSON round-trip via `type`/`role` discriminators; `Register`/`Resolve` provider registry; `Stream`/`Complete` helpers; SSE decoder handling `\r\n`/`\n`/`\r` + `event:`/`data:` fields; `partialjson.ParsePartial` never returns error.
- Non-functional: no `any` where specific types work; `json.RawMessage` for tool args/details; race-safe registry.

## Related Code Files
- Create: `internal/ai/types.go`, `events.go`, `json.go`, `registry.go`, `stream.go`, `models.go`, `sse/sse.go`, `partialjson/partial_json.go`, `partialjson/repair.go` + `*_test.go` each
- Delete (commit): old `internal/ai/*` skeleton files already deleted in working tree

## Implementation Steps
<!-- Updated: Validation Session 1 - V1 Go toolchain install added -->
0. Toolchain bootstrap: `go` is not installed in this environment — download latest official Go (≥1.26) tarball, install to `~/.local/go`, export PATH in shell profile; verify `go version` + adjust go.mod toolchain directive if needed.
1. `types.go`: Content union (TextContent/ImageContent/ThinkingContent/ToolCall), Message union (User/Assistant/ToolResult), Role/StopReason/ThinkingLevel/CacheRetention enums, Usage+Cost, Model+Caps+Price, ToolDef, Context, StreamOptions — field-for-field per brainstorm §4.
2. `events.go`: 13-variant StreamEvent union with marker methods + documented ordering contract (Start → grouped index triples → Done|Error, channel closed after).
3. `json.go`: `MarshalContent/UnmarshalContent`, `MarshalMessage/UnmarshalMessage` with discriminator injection; table-driven round-trip tests incl. unknown-type error.
4. `registry.go`: `Provider` interface, `Register` (init()-time), `Resolve`; mutex-guarded map.
5. `stream.go`: `Stream(ctx, model, c, opts)` resolves+delegates; `Complete` drains channel to final message, returns error for EventError.
6. `sse/sse.go`: `Decoder` over `io.Reader`: bufio with 1MiB line cap, accumulates `data:` until blank line, yields `{Event, Data}`; handles all three EOL styles, ignores `:` comments/`ping`.
7. `partialjson`: `ParsePartial([]byte) json.RawMessage` pipeline: parse → repair (escape raw control chars in strings, double invalid backslash escapes) → partial-parse (own ~150 LOC tokenizer: close open strings/arrays/objects) → `{}` fallback. Fuzz test (`go test -fuzz`) asserting never-panic + always-valid-JSON output.
8. `models.go`: catalog literals for claude-sonnet-4-6, claude-opus-4-8, claude-haiku-4-5 (context windows, pricing, caps); `Lookup(provider, id)`.

## Success Criteria
- [x] `go test -race ./internal/ai/...` green; fuzz corpus for partialjson runs clean 30s (7.5M execs)
- [x] JSON round-trip golden tests: marshal→unmarshal→deep-equal for every union member
- [x] SSE decoder golden test against captured Anthropic fixture bytes (incl. split-across-reads chunks 1/3/7 bytes)
- [x] Old skeleton deletion committed; package compiles standalone (`go build ./internal/ai/...`)

## Completion Notes (260603)
- Toolchain: Go 1.26.4 installed to `~/.local/go` (sha256-verified); PATH export to `~/.zshrc` denied by permission classifier — user to persist manually.
- Event union is 12 variants (brainstorm §4 verbatim); phase text said "13" — miscount in phase doc, §4 is design of record.
- Coverage: ai 92.8%, partialjson 92.0%, sse 97.5%. Code review: DONE, zero findings.
- Models catalog pricing literals unverified against live rates — flag for data-freshness check when cost reporting lands (phase 13).

## Risk Assessment
- Partial-JSON tokenizer subtle bugs → mitigate with fuzzing + port Pi's test cases (research §2.8).
- Union JSON design locked here — downstream phases depend; review field names against brainstorm §4 before merge.
