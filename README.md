<p align="center">
  <img src="logo_transparent.png" alt="mixi-agent logo" width="200">
</p>

<h1 align="center">mixi-agent</h1>

<p align="center">
  A production-grade coding agent CLI, written in Go.
</p>

<p align="center">
  <img alt="Go version" src="https://img.shields.io/badge/Go-1.26-00ADD8?logo=go&logoColor=white">
  <img alt="Status" src="https://img.shields.io/badge/status-in%20development-orange">
  <img alt="Platform" src="https://img.shields.io/badge/platform-linux%20%7C%20macos-lightgrey">
</p>

---

**mixi-agent** is a single-binary coding agent (`mixi`) architecturally inspired by the Pi coding agent and hardened where it has gaps: bounded queues, max-turn limits, panic recovery, a permission engine, session locking, and crash-safe persistence. It speaks to LLM providers over normalized streaming APIs and drives a full tool-using agent loop against your codebase.

## Features

### Implemented

- **Normalized AI core** — provider-agnostic `Content` / `Message` / `StreamEvent` type system with sealed unions and a provider registry
- **Anthropic provider** — SSE streaming, extended thinking, partial-JSON repair for in-flight tool calls, retry with jitter
- **Agent runtime loop** — two-level run loop (turns + follow-ups), parallel tool dispatch, steering and follow-up queues, lifecycle hooks, panic-isolated tool execution
- **JSON-Schema tool validation** — argument coercion plus LLM-readable error messages
- **Nine built-in tools** — `read`, `write`, `edit`, `bash` (with background jobs), `bash_output`, `kill_bash`, `grep`, `find`, `ls`
- **Session persistence** — append-only JSONL tree storage with branching/forking, deferred first write, file locking, and crash-tail recovery

### Roadmap

- CLI with print mode (first end-to-end binary)
- Context compaction & working set
- Permission engine for tool calls
- Interactive TUI (Bubble Tea)
- MCP client (stdio), subprocess extensions, RPC mode
- OpenAI provider, observability & replay, fault-injection hardening

## Architecture

```
┌───────────────────────────────────────────────┐
│              CLI / TUI / RPC (planned)        │
├───────────────────────────────────────────────┤
│ internal/agent     two-level runtime loop     │
│                    queues · retries · hooks   │
├──────────────┬──────────────┬─────────────────┤
│ internal/    │ internal/    │ internal/       │
│ tools        │ session      │ schema          │
│ 9 built-ins  │ JSONL tree   │ validation +    │
│ + job table  │ storage      │ coercion        │
├──────────────┴──────────────┴─────────────────┤
│ internal/ai    normalized types · registry    │
│ internal/ai/anthropic · sse · partialjson     │
└───────────────────────────────────────────────┘
```

| Package | Purpose |
|---------|---------|
| `internal/ai` | Provider-agnostic type system and provider registry |
| `internal/ai/anthropic` | Anthropic API provider (SSE streaming, thinking, retries) |
| `internal/agent` | Agent runtime: turns, tool dispatch, queues, events |
| `internal/schema` | JSON-Schema validation with coercion and readable errors |
| `internal/tools` | Built-in tools and shared infrastructure |
| `internal/session` | Append-only JSONL conversation storage with branching |

## Getting Started

### Prerequisites

- **Go 1.26+**
- **ripgrep (`rg`)** on `PATH` — required by the `grep` tool (`fd` is optional; a pure-Go fallback is built in)

### Build & Test

```bash
git clone https://github.com/user/mixi-agent.git
cd mixi-agent

go build ./...
go test -race ./...
```

> **Note:** the `mixi` CLI binary is not wired up yet — the current codebase ships the core packages (provider, agent loop, tools, sessions). The end-to-end CLI lands next on the roadmap.

## Design Highlights

- **Crash-safe sessions** — sessions are append-only JSONL trees; a `kill -9` mid-write is recovered on load, and files without a valid session header are never touched
- **Deferred writes** — abandoned empty sessions never litter your disk; nothing is written until the first message
- **Bounded everything** — steering/follow-up queues, turn limits (default 80), and output truncation all have hard caps
- **Sealed unions over `interface{}`** — every polymorphic type (content, messages, events, session entries) is a closed set with JSON discriminators

## Project Structure

```
mixi-agent/
├── internal/
│   ├── ai/            # core types, provider registry, anthropic, sse, partialjson
│   ├── agent/         # runtime loop, events, queues, hooks
│   ├── schema/        # JSON-Schema validation & coercion
│   ├── tools/         # built-in tools
│   └── session/       # JSONL tree session storage
├── docs/              # codebase summary, changelog, journals
├── scripts/           # development utilities
└── plans/             # implementation plans (local)
```

## Development

- `go test -race ./...` must stay green
- Files stay under ~200 lines where practical; snake_case filenames
- Conventional commits (`feat:`, `fix:`, `docs:`, …)

## License

Not yet licensed — license to be determined before first release.
