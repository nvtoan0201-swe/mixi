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
- ✓ Interactive TUI (Bubble Tea) with permission approval UI
- ✓ MCP client (stdio) with per-call timeouts and lifecycle management
- ✓ Subprocess extensions with JSONL-RPC transport, blocking tool_call gates, action API
- RPC mode with permission agent (phase 14)
- OpenAI provider, observability & replay, fault-injection hardening

## Architecture

```
┌────────────────────────────────────────────────────────┐
│         CLI / TUI / RPC (RPC planned phase 14)         │
├────────────────────────────────────────────────────────┤
│ internal/agent     two-level runtime loop              │
│                    queues · retries · hooks            │
├────────────┬────────────────┬──────────┬───────────────┤
│ internal/  │ internal/      │ internal/│ internal/mcp  │
│ tools      │ session        │ schema   │ + wire        │
│ 9 built-ins│ JSONL tree     │validation│ subprocess    │
│ + MCP tools│ storage        │ coercion │ I/O & protocol│
├────────────┴────────────────┴──────────┴───────────────┤
│ internal/ai    normalized types · registry             │
│ internal/ai/anthropic · sse · partialjson              │
└────────────────────────────────────────────────────────┘
```

| Package | Purpose |
|---------|---------|
| `internal/ai` | Provider-agnostic type system and provider registry |
| `internal/ai/anthropic` | Anthropic API provider (SSE streaming, thinking, retries) |
| `internal/agent` | Agent runtime: turns, tool dispatch, queues, events |
| `internal/schema` | JSON-Schema validation with coercion and readable errors |
| `internal/tools` | Built-in tools (9), MCP tools, shared infrastructure |
| `internal/session` | Append-only JSONL conversation storage with branching |
| `internal/mcp` | MCP client for subprocess tool integration (protocol, transport, lifecycle) |
| `internal/wire` | JSONL framing for subprocess I/O (1MiB line cap, atomic writes) |

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

Useful flags: `--model provider/id`, `-c` (continue last session), `--resume <id>`, `--no-save`, `--session-dir <dir>`, `--max-turns N`, `--permission-mode plan|prompt|auto-edit|yolo`, `--allow` (repeatable), `--deny` (repeatable), `--no-mcp` (disable MCP), `--log-level debug|info|warn|error`, `--verbose` (mirror logs to stderr), `--print-stats` (show usage summary). Bad flags exit `2`; run errors exit `1`; Ctrl-C aborts with `130`.

> **Permission Engine (Phase 9):** Print mode now enforces four permission modes; default (`prompt`) asks for approval on write/execute/mcp calls. Use `--permission-mode auto-edit` for read+write free, or configure persistent rules in `~/.mixi/settings.json` under the `permissions` block.

### Run (interactive TUI mode)

```bash
go build -o mixi ./cmd/mixi

# Launch TUI mode automatically when connected to a terminal (tty):
./mixi

# Force TUI mode explicitly:
./mixi --mode tui

# TUI with custom permission mode:
./mixi --permission-mode auto-edit

# Continue previous session in TUI:
./mixi -c
```

**Keymap (TUI mode):**

| Binding | Action |
|---------|--------|
| `Enter` | Send prompt or steer mid-run |
| `Alt+Enter` | Queue follow-up question |
| `Escape` | Interrupt running agent |
| `Ctrl+C` (double) | Quit TUI |
| `Shift+Tab` | Toggle thinking block collapse |
| `Ctrl+P` | Cycle through available models |
| `Ctrl+O` | Expand all tool cards (toggle) |
| `Ctrl+T` | Expand all thinking blocks (toggle) |
| `Ctrl+G` | Open input in `$EDITOR` |
| `Ctrl+L` | Redraw screen |
| `Up` / `Down` | Cycle through input history |
| `PgUp` / `PgDn` | Scroll transcript |
| Mouse wheel | Scroll transcript |

**Slash commands:**

Prefix input with `/` to trigger built-in commands:
- `/model` — show current model
- `/compact` — trigger context compaction
- `/pin <label>` — pin entries for retention
- `/tree` — show session tree
- `/permissions` — list active permission rules
- `/mode` — show current permission mode
- `/name <label>` — label the session
- `/cost` — show token usage and cost breakdown
- `/mcp` — show MCP server status and tool counts
- `/mcp reconnect <name>` — manually reconnect a failed MCP server
- `/quit` — gracefully exit

Commands like `/new`, `/resume`, `/fork` are available via CLI flags (see above).

**Permission approval modal (interactive):**

When the agent needs approval for a tool call:
- `a` — Deny this call
- `d` — Deny all similar calls
- `A` — Always allow this call (session grant)
- `Escape` — Dismiss modal and interrupt

Modal shows a colorized diff for `edit` calls, and a summary of command+args for `bash`/`mcp`.

**TUI logging:**

- Session logs go to `<sessiondir>/mixi.log`, never to stdout/stderr
- Logs are only emitted in verbose debug builds (not by default)

### Session Replay (Phase 13)

```bash
# Replay a recorded session as a text transcript with zero API calls:
./mixi replay ~/.mixi/sessions/<cwd-slug>/<date>_<id>.jsonl

# Control replay speed:
./mixi replay session.jsonl --speed 1x      # Real-time (default)
./mixi replay session.jsonl --speed 5x      # 5× faster (tool gaps capped at 2s)
./mixi replay session.jsonl --speed instant # No delays

# Stop at a specific entry:
./mixi replay session.jsonl --until <entryId>

# Works on live sessions (read-only lock):
./mixi replay -c session.jsonl  # Replay most recent session (non-blocking read)
```

`mixi replay` re-renders a JSONL session file as a plain-text transcript, simulating tool execution and thinking blocks without calling any APIs. Useful for post-hoc analysis, documentation, and debugging.

**Logging & Observability (Phase 13):**

- Structured JSON logs: `~/.mixi/logs/mixi-<YYYY-MM-DD>.jsonl` (rotated daily, keeps 7 days)
- Log level control: `--log-level debug|info|warn|error` (default: warn) or env `MIXI_LOG`
- `--verbose` mirrors logs to stderr in headless modes (all records in print/replay, WARN+ in TUI)
- Agent loop logs: turn lifecycle (turn start/end, model, stop_reason, tool_calls, latency_ms), tool execution (tool name, call_id, latency_ms, error flag)
- Usage tracking per turn: tokens (input/output), cost, model, duration; accessible via `/cost` command in TUI or `--print-stats` in print mode

### MCP Server Integration (Phase 11)

`mixi` supports integrating external tools via the [Model Context Protocol](https://modelcontextprotocol.io/).

**Configuration example** (`~/.mixi/settings.json`):
```json
{
  "mcpServers": {
    "github": {
      "command": "github-mcp-server",
      "args": ["stdio"],
      "env": {
        "GITHUB_TOKEN": "${GITHUB_TOKEN}"
      },
      "timeoutMs": 30000
    }
  }
}
```

**How it works:**
- Servers start on demand (first tool call or explicit `/mcp reconnect`)
- Tools appear with names like `mcp__github__search_repos` (pattern: `mcp__<server>__<tool>`)
- Per-call timeout: 30s default, overridable per-server in config
- Crash detection: if server crashes mid-call, error surfaced to agent ("MCP server X crashed…")
- Graceful shutdown: SIGTERM → 2s wait → SIGKILL on exit

**CLI:** use `--no-mcp` to skip MCP initialization (all servers disabled for the session).

**TUI:** `/mcp` shows server status (name, version, ready state, tool count); `/mcp reconnect <name>` manually reconnects a failed server.

## Design Highlights

- **Crash-safe sessions** — sessions are append-only JSONL trees; a `kill -9` mid-write is recovered on load, and files without a valid session header are never touched
- **Deferred writes** — abandoned empty sessions never litter your disk; nothing is written until the first message
- **Bounded everything** — steering/follow-up queues, turn limits (default 80), and output truncation all have hard caps
- **Sealed unions over `interface{}`** — every polymorphic type (content, messages, events, session entries) is a closed set with JSON discriminators

## Project Structure

```
mixi-agent/
├── cmd/mixi/          # CLI entry point (print mode, TUI, signals, session wiring, permissions)
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
│   ├── tui/           # Bubble Tea interactive terminal UI (bridge, transcript, approval modal)
│   └── modes/         # print mode + TUI mode + EventSink (text / JSONL)
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
