package main

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/user/mixi-agent/internal/agent"
	"github.com/user/mixi-agent/internal/ai"
	"github.com/user/mixi-agent/internal/config"
	"github.com/user/mixi-agent/internal/perm"
)

// buildPermissionEngine assembles the engine from merged settings + ad-hoc
// flag rules. A nil asker selects the headless default, which auto-approves
// only when the user explicitly chose a permissive mode; interactive modes
// pass an asker that routes ASKs to the UI.
func buildPermissionEngine(rc *config.RuntimeConfig, cwd string, asker perm.Asker) (*perm.Engine, error) {
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
	if asker == nil {
		asker = perm.HeadlessAsker{
			AllowAll: policy.Mode == perm.ModeYolo || policy.Mode == perm.ModeAutoEdit,
		}
	}
	return perm.NewEngine(policy, asker, cwd), nil
}

// agentNotifier breaks the construction cycle between the permission engine
// (built before the agent) and the agent event bus the TUI asker publishes
// on. Until the agent is set, asks are denied — they cannot legitimately
// happen before a run starts anyway.
type agentNotifier struct {
	mu sync.Mutex
	a  *agent.Agent
}

func (n *agentNotifier) set(a *agent.Agent) {
	n.mu.Lock()
	n.a = a
	n.mu.Unlock()
}

func (n *agentNotifier) publish(p perm.PendingAsk) {
	n.mu.Lock()
	a := n.a
	n.mu.Unlock()
	if a == nil {
		p.Reply <- perm.AskDeny
		return
	}
	a.Notify(agent.EvPermissionAsk{Req: p})
}

// notice forwards a user-facing text notice (MCP server disabled, …) onto
// the agent bus; notices raised before the agent exists are dropped — the
// subsystems that send them also log the underlying condition.
func (n *agentNotifier) notice(text string) {
	n.mu.Lock()
	a := n.a
	n.mu.Unlock()
	if a == nil {
		return
	}
	a.Notify(agent.EvNotice{Text: text})
}

// publishEvent forwards an arbitrary bus event (extension status segments,
// …); events raised before the agent exists are dropped.
func (n *agentNotifier) publishEvent(ev agent.Event) {
	n.mu.Lock()
	a := n.a
	n.mu.Unlock()
	if a != nil {
		a.Notify(ev)
	}
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
