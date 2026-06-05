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
- **Permission engine** — four modes (plan/prompt/auto-edit/yolo), allow/deny rules with glob syntax, baseline safety screens (dangerous bash patterns, write outside cwd, secret file access), session grants

### Roadmap

- ✓ CLI with print mode
- ✓ Context compaction & working set
- ✓ Permission engine for tool calls
- Interactive TUI (Bubble Tea) with permission approval UI
- MCP client (stdio), subprocess extensions, RPC mode with permission agent
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

### Run (print mode)

```bash
go build -o mixi ./cmd/mixi

# Live smoke test against the Anthropic API:
ANTHROPIC_API_KEY=sk-... ./mixi -p "create hello.txt containing hi"
cat hello.txt   # → hi; session JSONL lands under ~/.mixi/sessions/

# JSONL event stream, piped prompt, follow-ups:
echo "what does this repo do?" | ./mixi --output json
./mixi -p "list the Go files" --message "now count them" --print-stats

# Headless mode in default prompt-mode now asks for approval on writes:
# (reads are free, execute/mcp ask for approval unless --allow rules permit)
./mixi -p "edit main.go to add a comment" --permission-mode prompt
# → Denies write, asks via SIGINT-like message in headless mode (explicit deny)

# To allow writes without approval in headless mode:
./mixi -p "edit main.go to add a comment" --permission-mode auto-edit
# → Or: --allow 'edit(main.go)' in settings.json for persistent rules
```

Useful flags: `--model provider/id`, `-c` (continue last session), `--resume <id>`, `--no-save`, `--session-dir <dir>`, `--max-turns N`, `--permission-mode plan|prompt|auto-edit|yolo`, `--allow` (repeatable), `--deny` (repeatable). Bad flags exit `2`; run errors exit `1`; Ctrl-C aborts with `130`.

> **Permission Engine (Phase 9):** Print mode now enforces four permission modes; default (`prompt`) asks for approval on write/execute/mcp calls. Use `--permission-mode auto-edit` for read+write free, or configure persistent rules in `~/.mixi/settings.json` under the `permissions` block.

## Design Highlights

- **Crash-safe sessions** — sessions are append-only JSONL trees; a `kill -9` mid-write is recovered on load, and files without a valid session header are never touched
- **Deferred writes** — abandoned empty sessions never litter your disk; nothing is written until the first message
- **Bounded everything** — steering/follow-up queues, turn limits (default 80), and output truncation all have hard caps
- **Sealed unions over `interface{}`** — every polymorphic type (content, messages, events, session entries) is a closed set with JSON discriminators

## Project Structure

```
mixi-agent/
├── cmd/mixi/          # CLI entry point (print mode, signals, session wiring, permissions)
├── internal/
│   ├── ai/            # core types, provider registry, anthropic, faux, sse, partialjson
│   ├── agent/         # runtime loop, events, queues, hooks, tool filters
│   ├── schema/        # JSON-Schema validation & coercion
│   ├── tools/         # built-in tools
│   ├── session/       # JSONL tree session storage
│   ├── config/        # settings files, flags, precedence resolution
│   ├── perm/          # permission engine (modes, rules, decision pipeline)
│   ├── compact/       # context compaction (estimator, cutter, summarizer)
│   ├── workset/       # working-set assembly (file tracking, budget pipeline)
│   └── modes/         # print mode + EventSink (text / JSONL)
├── docs/              # codebase summary, changelog, journals
├── scripts/           # development utilities
└── plans/             # implementation plans (local)
```

## Development

- `go test -race ./...` must stay green
- End-to-end CLI tests run offline: `--model faux/scripted` replays JSON-scripted turns from `MIXI_FAUX_SCRIPT` (see `cmd/mixi/e2e_test.go`); the live Anthropic call above is a manual smoke only
- Files stay under ~200 lines where practical; snake_case filenames
- Conventional commits (`feat:`, `fix:`, `docs:`, …)

## License

Not yet licensed — license to be determined before first release.
