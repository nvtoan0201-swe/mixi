# mixi-agent Codebase Summary

**Module:** `github.com/user/mixi-agent` | **Status:** Phase 4 complete (agent runtime loop + tools interface)

## Package Overview

| Package | Purpose | Key Types |
|---------|---------|-----------|
| `internal/ai` | Normalized type system for all providers; provider registry | `Content`, `Message`, `StreamEvent`, `Model`, `Provider` |
| `internal/ai/anthropic` | Anthropic API provider (SSE streaming, thinking, retries) | `Provider`, `BlockTracker`, SSE event mapping |
| `internal/ai/sse` | Server-Sent Events decoder | `Decoder` |
| `internal/ai/partialjson` | Streaming partial JSON repair | `RepairContext` |
| `internal/agent` | Two-level runtime loop: turns, tool dispatch, queues, retry, events | `Agent`, `runLoop`, `Event` (sealed union) |
| `internal/agent/agenttest` | Scripted test provider (fake stream generator) | `FakeProvider` |
| `internal/schema` | JSON-Schema validation, coercion, LLM-readable errors | `Compile`, `Coerce`, `ErrFormatter` |
| `internal/tools` | Tool interface, registry, execution dispatch | `Tool`, `Registry`, `ToolResult` |

## Architecture Layers

### Layer 1: Core Types (`internal/ai`)
- **Sealed unions** (marker interfaces + JSON discriminators): `Content`, `Message`, `StreamEvent`
- **Provider abstraction**: `Provider` interface (register at init, stream events, close on Done/Error)
- **Models:** global registry of known model IDs + defaults (claude-3.5-sonnet, gpt-4, etc.)
- **No imports of other internal packages** — type system is self-contained

### Layer 2: Provider Implementation (`internal/ai/anthropic`)
- Converts Anthropic wire format → normalized `StreamEvent` order contract
- Handles extended thinking (redacted_thinking), block tracking, content indexing
- SSE decoder + partial JSON repair for streaming text/tool calls
- Caches model metadata; retry hooks apply to API calls

### Layer 3: Agent Runtime (`internal/agent`)
- **Two-level loop structure:**
  1. **Outer:** follow-up extend a run; loop restarts when follow-ups enqueued
  2. **Inner:** stream assistant turn → extract tool calls → dispatch tools → collect results → advance turn counter
- **Message types:**
  - `ModelMessage` (ai.Message) — provider-level user/assistant/toolResult
  - `BashExecution`, `CompactionSummary`, `BranchSummary` — harness items
  - `AgentMessage` interface (sealed union) — all of above
- **Event contract** (per-run order, documented in `events.go`):
  ```
  EvAgentStart → [per turn: EvTurnStart → EvMessageStart → EvMessageUpdate* →
  EvMessageEnd → EvToolStart/End* → EvTurnEnd] → EvAgentEnd
  EvRetryStart/End may precede a turn's EvMessageStart
  ```
- **Queues:** steering (inject mid-turn), follow-up (extend run after completion), bounded capacity (ErrQueueFull)
- **Retry:** transient errors (network) retry with jitter; fatal errors (model not found) end run immediately
- **Hooks:** `TransformContext`, `GetAPIKey`, `BeforeToolCall`, `AfterToolCall`, `ShouldStop` — applied per turn/call
- **Tool execution:** parallel by default, sequential if any tool declares `ExecSequential` or config forces it
- **Panic recovery:** tool panics → error result, run continues

### Layer 4: Tool Interface (`internal/tools`)
- `Tool` interface: `Name()`, `Description()`, `Schema()`, `Mode()`, `Execute()`
- `Registry`: name-keyed, build-once, read-safe
- `ToolResult`: content blocks (text/image) + metadata (Details, IsError)
- `ToolUpdate`: streaming updates from long-running tools (non-blocking send via select+default)

### Layer 5: Schema Validation (`internal/schema`)
- **Compile:** JSON Schema → validator, rejects unknown keywords
- **Coerce:** type promotion (string→number, null→empty string), unsets bad fields
- **ErrFmt:** LLM-readable validation errors (no internal stack traces)

## Message Flow

1. **User calls `Agent.Prompt(context, message)`**
   - Message queued to steering, run started if idle
2. **runLoop pulls steering queue** → emit EvMessageStart/End → inject into history
3. **Stream LLM** → receive AssistantMessage, emit EvMessageUpdate* (render-only)
4. **Extract tool calls** from message
5. **executeToolCalls:**
   - Parallel: start all calls, collect as each finishes (order: completion order in race)
   - Sequential: start one, collect, repeat (order: source order)
6. **Emit EvToolStart/End** per call, emit EvToolUpdate* if tool streams
7. **Emit EvTurnEnd** with full message + results
8. **Check steering queue:** if new messages, loop back to step 2
9. **Check follow-up queue:** if messages and no tool calls were made, continue one more turn
10. **Emit EvAgentEnd** with all new messages + end reason (Done, MaxTurns, Error, Aborted)

## Test Coverage

- **25 test files** across all packages
- **68+ passing tests** (agent/loop/toolexec/retry/hooks, AI types/events/registry/stream, schema validation/coercion)
- **Race detector:** green (no data races detected)
- **Goleak:** green (no goroutine leaks)

## Key Files

### Agent Runtime (3,000+ LOC)
- `agent.go` (Config, Agent type, New, Prompt/Continue/Subscribe methods)
- `loop.go` (runLoop, inner/outer loop logic, stream handling)
- `toolexec.go` (executeToolCalls, parallel/sequential dispatch, panic recovery)
- `events.go` (sealed Event union, event order contract)
- `messages.go` (AgentMessage union, ModelMessage, BashExecution, CompactionSummary)
- `queue.go` (boundedQueue, DrainAll/DrainOne modes)
- `retry.go` (classifyError, retryDelay, jitter + retry-after)
- `sysprompt.go` (BuildSystemPrompt, tool definitions, context file embedding)
- `hooks.go` (Hooks interface, no-op defaults)
- `main_test.go` (test helpers, scripted stream runner)

### AI Layer (500+ LOC)
- `types.go` (Content/Message/StreamEvent sealed unions, Model, Context)
- `json.go` (JSON marshal/unmarshal with discriminators)
- `events.go` (StreamEvent order contract)
- `registry.go` (Provider registry, Resolve, Stream)
- `models.go` (Known models, LookupModel)
- `stream.go` (Stream func, error stream fallback)

### Anthropic Provider (500+ LOC)
- `provider.go` (Provider impl, Anthropic API call)
- `request.go` (RequestMessage builder, tool definitions)
- `convert.go` (Anthropic wire → normalized StreamEvent)
- `events.go` (Anthropic event types: text_start, thinking_delta, tool_use_start, etc.)
- `block_tracker.go` (Content block index tracking during streaming)

### Schema Validation (300+ LOC)
- `schema.go` (Compile: JSON Schema → validator)
- `coerce.go` (Type promotion, field unsetting)
- `errfmt.go` (LLM-readable error formatting)

## Configuration

`Config` struct (agent.go):
```go
Model        ai.Model              // Required: model ID + provider
Tools        *tools.Registry       // Default: empty registry
Hooks        Hooks                 // Default: no-op
SystemPrompt string                // Default: empty (BuildSystemPrompt fills)
StreamOpts   ai.StreamOptions      // Default: empty (provider decides)
MaxTurns     int                   // Default: 80 (0 = unlimited, <0 = 80)
SequentialTools bool               // Default: false (parallel)
SteerDrain   DrainMode             // Default: DrainAll
Stream       StreamFunc            // Default: registryStream (provider lookup)
Log          *slog.Logger          // Default: slog.Default()
```

## Concurrency Model

- **Agent methods:** Prompt/Continue/Subscribe/Abort guarded by single mutex
- **Event bus:** queues subscribers per channel capacity (16 default), drops render events on overflow
- **Tool execution:** parallel goroutines with atomic counters + WaitGroup (no shared tool state assumed)
- **Queues:** mutex-guarded FIFO (steering/follow-up)
- **No RwMutex:** provider registry uses RwMutex, agent loop holds no state between calls

## Integration Points

1. **Provider lookup:** `ai.Resolve(model.API)` → error if not registered
2. **Tool dispatch:** `tools.Registry.Get(callName)` → nil if not found (error result)
3. **Schema validation:** `schema.Compile(tool.Schema())` → LLM-readable errors on bad args
4. **Hooks:** called at transform context / get API key / before/after tool call / should stop checkpoints
5. **Logging:** structured logs to Config.Log

## Stability Notes

- **Backward compatibility:** all wire types (ai.Message) are versioned and can evolve via new discriminator values
- **Extension points:** Tool interface is stable; new tool types registered at startup
- **Provider evolution:** new providers register themselves; existing logic unaffected
- **Schema validation:** coercion is conservative (promote types safely, omit bad fields rather than fail)
