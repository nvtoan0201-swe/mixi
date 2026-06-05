package main

import (
	"context"
	"encoding/json"

	"github.com/user/mixi-agent/internal/agent"
	"github.com/user/mixi-agent/internal/ai"
	"github.com/user/mixi-agent/internal/config"
	"github.com/user/mixi-agent/internal/perm"
)

// buildPermissionEngine assembles the engine for a headless run: merged
// settings + ad-hoc flag rules, and an asker that auto-approves only when
// the user explicitly chose a permissive mode.
func buildPermissionEngine(rc *config.RuntimeConfig, cwd string) (*perm.Engine, error) {
	policy, err := perm.NewPolicy(perm.PolicyConfig{
		Mode:         rc.PermissionMode,
		Allow:        rc.Setting.Permissions.Allow,
		Deny:         rc.Setting.Permissions.Deny,
		DenyPatterns: rc.Setting.Permissions.DenyPatterns,
		FlagAllow:    rc.Flags.Allow,
		FlagDeny:     rc.Flags.Deny,
	})
	if err != nil {
		return nil, err
	}
	asker := perm.HeadlessAsker{
		AllowAll: policy.Mode == perm.ModeYolo || policy.Mode == perm.ModeAutoEdit,
	}
	return perm.NewEngine(policy, asker, cwd), nil
}

// permissionFilter adapts the permission engine onto the agent's tool-call
// filter pipeline.
type permissionFilter struct {
	eng *perm.Engine
}

func (p permissionFilter) FilterToolCall(ctx context.Context, call ai.ToolCall) (json.RawMessage, *agent.BlockDecision, error) {
	res := p.eng.Decide(ctx, call)
	if res.Allow {
		return call.Args, nil, nil
	}
	return call.Args, &agent.BlockDecision{Reason: res.Reason}, nil
}
