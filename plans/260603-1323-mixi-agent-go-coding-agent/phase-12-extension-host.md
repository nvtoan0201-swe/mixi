---
phase: 12
title: "Extension Host"
status: completed
priority: P2
effort: "4d"
dependencies: [4, 11]
---

# Phase 12: Extension Host

## Overview
`internal/ext`: subprocess JSONL-RPC extension host — discovery, hello handshake, event subscription fan-out, blocking tool_call gates (5s timeout, 3 strikes), extension-registered tools/commands, action API, crash isolation with restart/disable. Reuses `internal/wire`.

## Context Links
- Brainstorm §13 (wire protocol table, Extension/API interfaces, lifecycle sequence, isolation rules — implement exactly)
- Research §9 (Pi's event taxonomy = the contract to preserve)

## Requirements
- Discovery: settings `extensions` map + executables in `~/.mixi/extensions/` and `.mixi/extensions/`; `--no-extensions` skip.
- Handshake: spawn → `hello` within 5s (else kill) → validate (tool conflicts: built-ins win, first-registered wins among extensions; reserved events) → `ready{sessionId,cwd,mode,model}`.
- Delivery: per-extension FIFO queue (cap 256, drop-oldest+WARN) for non-blocking events; blocking `tool_call` delivered sequentially in registration order, 5s response timeout → no-op + strike, 3 strikes → demote to non-blocking; mutations chain, first block wins.
- Actions: send_message (steer|followUp|nextTurn), append_entry, set_status, notify, ask_select, register_tool (hello-only), register_command.
- Isolation: crash → restart 1s/2s/4s, 3 strikes → Disabled + EvNotice; in-flight blocking gate fails open; in-flight ext tool call → IsError; 10 malformed msgs/min → Disabled.
- Two sample extensions in `examples/extensions/` as conformance fixtures: `permission_gate.go` (blocks rm -rf via tool_call) and `status_line.sh` (sets footer status) — exercised by host tests.

## Related Code Files
- Create: `internal/ext/host.go`, `protocol.go`, `extension.go` + tests; `examples/extensions/permission_gate/main.go`, `examples/extensions/status_line/status_line.sh`
- Modify: `cmd/mixi/main.go` (wiring), `internal/agent/loop.go` (ext EventFilter after perm engine), `internal/tui` (status segments, ext commands in autocomplete)

## Implementation Steps
1. `protocol.go`: message structs per §13 table; version field `protocol:1`.
2. `extension.go`: per-ext state machine (Starting/Ready/Degraded/Crashed/Disabled), queue, strike counters.
3. `host.go`: discovery, spawn/handshake, fan-out by subscription, blocking-gate pipeline, action dispatch to API impl, shutdown broadcast (3s then SIGKILL group).
4. API impl: bridges to agent (SendUserMessage→queues), session (append_entry), TUI sink (set_status/notify/ask_select — headless: notify→stderr, ask_select→first option + WARN).
5. Conformance tests: fake extension binary (Go, scriptable): subscribe-all echo, slow-responder (timeout strikes), crasher (restart→disable), tool registrant (call round-trip), blocker (gate denies bash).
6. Lifecycle-order test: events received in §13 sequence (ready → session_start → agent events → session_shutdown).

## Success Criteria
- [x] Conformance suite green: handshake, gate block, mutation chain, timeout strikes, crash restart/disable, tool round-trip
- [x] Hung extension cannot stall a tool call >5s (measured in test)
- [x] permission_gate example blocks `rm -rf /tmp/x` end-to-end with fake provider
- [x] Host survives extension writing 10MB garbage line (wire cap) — extension disabled, agent unaffected

## Risk Assessment
- Protocol is a public contract once shipped → mark `protocol:1` experimental in docs until phase 16 sign-off.
- Blocking-gate latency stacking with many extensions → sequential by design (correctness); document budget (N ext × 5s worst case), revisit post-v1.
