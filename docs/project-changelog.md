# Project Changelog

## Phase 1: AI Core Types & Streaming (Commit add2cf0)
**Date:** June 3, 2025

- Implement AI layer with normalized type system: sealed unions for Content (text/image/thinking/tool), Message (user/assistant/toolResult), StreamEvent
- Define Provider interface and global registry (Register, Resolve)
- Add Model type + known models (claude-3.5-sonnet, gpt-4, etc.)
- Implement JSON marshal/unmarshal with discriminators for wire protocol
- Build SSE (Server-Sent Events) decoder for streaming responses
- Add partial JSON repair for in-flight tool-call argument parsing
- Create stream event order contract (EventStart, Delta, End, Done/Error terminal)
- 14 tests, all passing

## Phase 2: Anthropic Streaming Provider (Commit 3ff6ddd)
**Date:** June 3, 2025

- Implement Anthropic API provider conforming to Provider interface
- Add block tracker for streaming content block indices
- Convert Anthropic wire format to normalized StreamEvent sequence
- Support extended thinking (redacted_thinking flag, signature preservation)
- Handle text streaming, tool-use (tool calls), and thinking blocks
- Implement request builder for model calls (RequestMessage)
- Add retry hooks (transient vs fatal error classification)
- Add API key hook for per-call credential resolution
- 15 tests including provider round-trip and live integration points, all passing

## Phase 3: JSON-Schema Tool-Arg Validation (Commit 17b7075)
**Date:** June 4, 2025

- Add schema validation: compile JSON Schema to runtime validator
- Implement type coercion (string→number, null→empty string, object unset on type mismatch)
- Build LLM-readable error formatter (no internal stack traces, field + constraint focus)
- Create tool argument validation in execution path (pre-Execute)
- Add validation tests: coerce edge cases, error messages, schema compilation
- 6 tests, all passing

## Phase 4: Agent Runtime Loop & Tools Interface (Commit pending, today 2026-06-04)

### Agent Runtime (`internal/agent`)
- Implement two-level agent loop: outer (follow-ups extend run) and inner (stream turn, dispatch tools, retry)
- Add Agent type and Config (model, tools registry, hooks, system prompt, max-turns)
- Implement Prompt/Continue/Subscribe/Abort public API
- Create message queues: steering (inject mid-turn), follow-up (extend after completion), bounded capacity
- Build retry logic: classify errors (transient vs fatal), exponential backoff with jitter, retry-after honor
- Add event bus: multiplex subscribers, drop render events on overflow, disconnect slow subscribers
- Implement event order contract (EvAgentStart → turns → EvAgentEnd, with turn/tool/message/retry sub-events)
- Create AgentMessage sealed union (ModelMessage, BashExecution, CompactionSummary, BranchSummary)
- Build system prompt assembler (tool definitions, context files, ordered injection)
- Add hook system: TransformContext, GetAPIKey, BeforeToolCall, AfterToolCall, ShouldStop
- 34 tests: agent lifecycle, queue limits, retries, hooks, events, streaming, concurrency; all passing

### Tools Interface (`internal/tools`)
- Define Tool interface: Name, Description, Schema (JSON), Mode (Parallel/Sequential), Execute (with streaming updates)
- Create Registry (name-keyed, build-once, read-safe)
- Add ToolResult type: content blocks + metadata + error flag
- Implement tool execution dispatch: parallel (goroutines) vs sequential (one at a time, source order)
- Add execution batching: any ExecSequential tool forces whole batch sequential
- Build panic recovery: recover panics → error result, emit tool events, continue run
- Add tool updates: streaming tools can emit partial output (non-blocking via select+default)
- 10 tests: execution order, parallel/sequential dispatch, panic recovery, unknown tool, updates; all passing

### Test Infrastructure
- Create agenttest package with FakeProvider for scripted streaming (test harness)
- Add test helpers for event stream validation
- Implement race detector green, goleak green

**Summary:** 44 tests in Phase 4 (agent + tools), 68+ total tests across all phases. Runtime loop fully functional: multi-turn conversations, parallel/sequential tool execution, steering/follow-up queues, retries with backoff, event streaming to subscribers, hook extensibility.

### Test Results
```
ok  github.com/user/mixi-agent/internal/agent       2.2s  (34 tests)
ok  github.com/user/mixi-agent/internal/tools       0.0s  (10 tests)
ok  github.com/user/mixi-agent/internal/schema      0.0s  (6 tests)
ok  github.com/user/mixi-agent/internal/ai          0.0s  (6 tests + subtests)
ok  github.com/user/mixi-agent/internal/ai/anthropic 0.0s  (7 tests)
ok  github.com/user/mixi-agent/internal/ai/sse      0.0s  (2 tests)
ok  github.com/user/mixi-agent/internal/ai/partialjson 0.0s (2 tests)
PASS: race detector, goleak
```

All phases (1–4) delivered on schedule. Foundation complete for permission/session/extension layers (Phase 5+).
