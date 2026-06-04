# Schema Validation Milestone Complete

**Date**: 2026-06-04 10:00
**Severity**: High (foundational layer)
**Component**: internal/schema package
**Status**: Resolved

## What Happened

Completed implementation of `internal/schema` package (commit 17b7075) — a JSON-Schema draft 2020-12 validator with pre-validation coercion and LLM-readable error formatting for tool argument validation. Package provides core validation plumbing the AI layer depends on to safely execute tool calls with arguments the model sends.

## The Brutal Truth

This is solid work. No firefighting, no shortcuts, no "we'll fix it later" debt. The implementation compiled clean on first build, tests pass under race conditions, and the design trades off correctness vs. coverage in documented, defensible ways. It's boring in the best sense — it does exactly what it claims and doesn't pretend to be more.

## Technical Details

**Package**: `internal/schema` — 6 files, ~3.8k LOC
- `schema.go`: Validator core, sha256-keyed cache (sync.Map), union member pre-compilation
- `coerce.go`: Type coercion walk (numeric strings→number, "true"/"false"/1/0→bool, number→string, recursive)
- `errfmt.go`: Pi-compatible error reports (fieldPath + violation, 10-bullet cap, echoes original uncoerced args)
- Test files: 9 builtins (read, write, edit, bash, bash_output, kill_bash, grep, find, ls) + 20+ coercion cases

**Dependency**: `github.com/santhosh-tekuri/jsonschema/v6 v6.0.2`

**Key implementation detail**: Tool name passed per-call to `ValidateAndCoerce(toolName string, args json.RawMessage)` rather than stored on Validator. Cache is keyed by schema hash (sha256) and shared across tools — embedding tool name would break the sharing.

**Union handling**: anyOf/oneOf members pre-compiled via JSON-pointer URL fragments (`resourceURL + "#" + ptr`). Probe confirmed the library expects RFC 6901 escaping (~0/~1) for special chars in fragments, not percent-encoding — implemented in `escapePointerToken()`.

**Coercion strategy**: Walk the schema tree, attempt conversions only when explicitly lossless (numeric string→number validated via re-decode, "true"/"false"/1/0→bool, json.Number→string). Union members tried on deep copies to prevent leak of partial mutations if a member coerces but then fails validation.

**Error format**: Validation failure renders as:
```
Validation failed for tool "<name>":
  - <fieldPath>: <message>
  - ...

Received arguments:
<pretty JSON (original, uncoerced)>
```

Leaf violations only (intermediate nodes like "doesn't validate with anyOf" suppressed), max 10 bullets to prevent context flood.

## What We Tried

Single approach — design felt solid from first principles (async validation loop vs. coercion; cache by schema hash; pre-compile unions; echo original args on error). Implemented, tested, shipped.

## Root Cause Analysis

N/A. No failures this phase.

## Lessons Learned

1. **Pre-compilation pays for unions**: Coercion walk happens per-call; pre-compiling union member subschemas at build-time eliminates re-parse cost.

2. **JSON pointer escaping matters**: RFC 6901 (~0/~1) is non-obvious. Verified empirically by constructing a fragment with a `/` in a property name and confirming the library rejects percent-encoded form.

3. **Echo the original on error**: LLMs fix things when they see what they sent. Showing the coerced form would hide the actual mistake (e.g., sent string "5" for int, but error shows the coerced 5 — model can't learn). Coercion is opaque sugar; validation is the only source of truth.

4. **Cache correctness**: Sharing a Validator (including its pre-compiled union schemas) across tools works because coercion is deterministic and schema-driven. Tool name only appears in error reports, never in logic.

## Next Steps

**Deferred (documented, not blocking)**:
- Coercion currently skips `additionalProperties` as schema and `patternProperties` $ref unions — both rare in tool schemas, and validation catches them anyway (fail-safe). Revisit when MCP dynamic schemas land and tool definitions become more complex.
- No `prefixItems` or other draft 2020-12 exotic keywords tested in practice yet; coverage adequate for agent use case.

**No immediate action required** — package is production-ready.

---

**Status:** DONE
**Summary:** Schema validation package complete, tested (9 builtins + 20+ coercion cases, race-clean), designed for correctness and LLM readability. No setbacks this phase.
**Concerns:** None — known design gaps (additionalProperties, patternProperties) documented and fail-safe.
