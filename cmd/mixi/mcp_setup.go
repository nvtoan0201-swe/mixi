package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/user/mixi-agent/internal/config"
	"github.com/user/mixi-agent/internal/mcp"
	"github.com/user/mixi-agent/internal/tools"
)

// startMCP brings up the configured MCP servers and blocks (bounded) until
// their first handshakes settle, so tools reach the registry — and the
// system prompt — before the agent is built. Returns nil when MCP is off.
// Slow or crashed servers keep retrying in the background; their tools
// appear on later turns via the per-turn tool defs.
func startMCP(rc *config.RuntimeConfig, reg *tools.Registry, log *slog.Logger,
	notify func(string)) (*mcp.Manager, error) {
	if rc.Flags.NoMCP || len(rc.Setting.MCPServers) == 0 {
		return nil, nil
	}
	cfgs, err := mcp.ParseServers(rc.Setting.MCPServers)
	if err != nil {
		return nil, err
	}
	mgr, err := mcp.NewManager(mcp.ManagerOptions{
		Servers:  cfgs,
		Registry: reg,
		Log:      log,
		Notify:   notify,
	})
	if err != nil {
		return nil, err
	}
	mgr.Start(context.Background())

	// Handshakes self-bound by each server's call timeout; the extra second
	// absorbs spawn overhead.
	var wait time.Duration
	for _, c := range cfgs {
		if t := c.Timeout(); t > wait {
			wait = t
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), wait+time.Second)
	defer cancel()
	mgr.WaitInitial(ctx)
	return mgr, nil
}
