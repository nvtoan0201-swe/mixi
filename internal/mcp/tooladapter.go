package mcp

import (
	"context"
	"encoding/json"

	"github.com/user/mixi-agent/internal/schema"
	"github.com/user/mixi-agent/internal/tools"
)

// permissiveSchema accepts any object; used when a server schema is absent
// or cannot be compiled locally (the server validates authoritatively).
var permissiveSchema = json.RawMessage(`{"type":"object"}`)

// checkSchema verifies a tool's inputSchema compiles with the local
// validator; otherwise the adapter serves a permissive schema so the tool
// stays callable — argument validation falls to the server.
func checkSchema(m *Manager, srvName string, info ToolInfo) ToolInfo {
	if len(info.InputSchema) == 0 {
		info.InputSchema = permissiveSchema
		return info
	}
	if _, err := schema.Compile(info.InputSchema); err != nil {
		m.log.Warn("mcp: tool schema does not compile locally, serving permissive schema",
			"server", srvName, "tool", info.Name, "err", err)
		info.InputSchema = permissiveSchema
	}
	return info
}

// adaptedTool projects one MCP server tool into the agent's tool registry.
// It holds no catalog data itself: description and schema are read from the
// manager's last-known state, so a re-list refreshes them without
// re-registration.
type adaptedTool struct {
	m       *Manager
	server  string // raw server key
	orig    string // tool name as the server knows it
	regName string // namespaced registry name
}

func (t *adaptedTool) Name() string { return t.regName }

func (t *adaptedTool) Description() string {
	info, _ := t.m.toolInfo(t.server, t.orig)
	return "[mcp:" + t.server + "] " + info.Description
}

func (t *adaptedTool) Schema() json.RawMessage {
	info, ok := t.m.toolInfo(t.server, t.orig)
	if !ok || len(info.InputSchema) == 0 {
		return permissiveSchema
	}
	return info.InputSchema
}

// Mode is parallel: MCP servers handle their own concurrency, and one slow
// server must not serialize unrelated built-in calls.
func (t *adaptedTool) Mode() tools.ExecMode { return tools.ExecParallel }

func (t *adaptedTool) Execute(ctx context.Context, args json.RawMessage, _ chan<- tools.ToolUpdate) (tools.ToolResult, error) {
	return t.m.callTool(ctx, t.server, t.orig, args)
}
