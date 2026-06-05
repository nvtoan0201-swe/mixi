---
phase: 11
title: "MCP Client"
status: completed
priority: P1
effort: "4d"
dependencies: [4, 9]
---

# Phase 11: MCP Client

## Overview
`internal/mcp` + shared `internal/wire`: MCP client (tools over stdio, protocol rev 2025-06-18 w/ 2025-03-26 fallback), JSON-RPC id-tracked framing, server lifecycle state machine with restart backoff, `mcp__server__tool` namespacing, crash-mid-call → LLM-visible error. Closes Pi's biggest functional gap.

## Context Links
- Brainstorm §11 (config, Transport, lifecycle diagram, namespacing, crash handling — implement exactly), §5 #5
- Research §8 (confirms greenfield — nothing to port)

## Requirements
- `wire`: Reader (bufio, 1MiB line cap) + Writer (mutex, optional deadline) over io pipes — shared with phases 12/14.
- Transport interface per §5; stdio impl: Setpgid spawn, stderr→DEBUG log, Close = shutdown notif → stdin close → 2s → SIGTERM → 2s → SIGKILL.
- Client: atomic id counter, `pending map[int64]chan *Response`, reader-goroutine routing, per-call timeout (config, 30s default), `notifications/tools/list_changed` → re-list, unknown notifications logged.
- Manager lifecycle: CONFIGURED→INITIALIZING→READY→RESTARTING (1s/2s/4s, 3 strikes→FAILED)→CLOSED; READY→RESTARTING fails all pending; tools marked unavailable (error result, not deregistered) during restart.
- Namespacing: `mcp__<sanitized-server>__<tool>`; inputSchema passthrough as Tool.Schema(); description prefix `[mcp:<server>] `; mcp category in perm engine (already mapped phase 9).
- Crash mid-call: pending → IsError ToolResult "MCP server X crashed during call. It is restarting; retry the tool if needed."
- `/mcp` command data: server states + tool counts + reconnect action.

## Related Code Files
- Create: `internal/wire/jsonl.go`, `internal/mcp/client.go`, `transport.go`, `stdio.go`, `protocol.go`, `manager.go`, `tooladapter.go` + tests; `testdata/fake_mcp_server.go` (test binary: scriptable responses, crash-on-command)
- Modify: `cmd/mixi/main.go` (mcpServers config wiring, `--no-mcp`), `internal/tui` (`/mcp` command)

## Implementation Steps
1. `wire/jsonl.go` w/ size-cap + concurrent-writer tests.
2. `protocol.go`: Request/Response/Notification/RPCError structs.
3. `stdio.go`: transport impl + kill-escalation test.
4. `client.go`: handshake (initialize w/ protocolVersion+capabilities, initialized notif), tools/list, tools/call (content → []ai.Content mapping: text/image; other content types → JSON-stringified text + WARN), id routing, timeouts.
5. `manager.go`: state machine + backoff; chaos test: kill -9 fake server mid-call → assert error result text, restart, second call succeeds.
6. `tooladapter.go`: tools.Tool impl delegating to client; unavailable-state error.
7. Conformance: integration test against `modelcontextprotocol/servers` everything-server (node, gated `-tags=mcp_integration`); fake server for CI.

## Success Criteria
- [x] Fake-server suite: handshake, list, call, timeout, crash-mid-call, restart, 3-strike disable — all green in CI
- [x] everything-server smoke: tools listed + echo tool callable end-to-end (local, tag-gated)
- [x] Tool names valid per Anthropic charset; collision-by-construction test (two servers, same tool name)
- [x] Permission ASK fires for mcp category with pretty-args preview

## Completion Notes (260605)
- `internal/wire`: Reader reassembles >64KiB lines under the 1MiB cap and discards over-long lines to the next message boundary; Writer is one atomic write per message with optional write deadline.
- Client `failAll` routes a true `ErrConnClosed` sentinel (a synthetic RPCError lost `errors.Is` matching — caught by the chaos test); pending channels are buffered(1) with delete-under-lock ownership.
- Lifecycle: spawn error → FAILED immediately (a missing binary won't fix itself); handshake/crash failures strike with 1s/2s/4s backoff, >3 consecutive → FAILED + user notice + `/mcp reconnect`; strikes reset after 30s stable uptime.
- Re-list/list_changed: adapters serve description/schema live from last-known catalog state; removed tools stay registered and error ("no longer provided") — the LLM-visible tool list never churns mid-run. New tools register late via the Registry RWMutex (contract touch, approved) and appear next turn through per-turn `Defs()`.
- Schema-coercion carry-forward resolved: uncompilable/absent MCP inputSchemas fall back to a permissive `{"type":"object"}` with WARN — server validates authoritatively; coercion not extended (approved).
- Chaos via a compiled scriptable fake server (testdata): crash tool exits mid-call (client-equivalent of kill -9); review fixes: notify-before-FAILED ordering, write-deadline on stdin Send, Reconnect shutdown guard.
- everything-server conformance ran live: 13 tools listed, echo verified (tag `mcp_integration`, npx).
- `cmd/mixi`: `startMCP` waits initial handshakes (bounded by per-server timeout) so tools reach the system prompt; `agentNotifier.notice` carries MCP notices onto the agent bus; `--no-mcp` e2e-tested.

## Risk Assessment
- Protocol version drift → pin 2025-06-18, fallback negotiation tested against fake server claiming older rev.
- Servers writing non-JSON to stdout → reader skips+WARNs malformed lines (cap 10/min then restart).
- Carry-forward from phase 3 review: `internal/schema` coercion skips `additionalProperties`-as-schema, `patternProperties`, `$ref`/`$defs` unions (validation still authoritative — degraded UX only). MCP server schemas may use these; decide whether to extend coercion here.
