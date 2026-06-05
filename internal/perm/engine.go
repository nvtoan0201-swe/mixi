package perm

import (
	"context"
	"fmt"

	"github.com/user/mixi-agent/internal/ai"
)

// SandboxSpec is the per-call kernel-sandbox request. v1 returns nil always;
// the field exists so bash execution can adopt landlock/seccomp without an
// interface change.
type SandboxSpec struct{}

// Result is one permission decision. Reason is set when the call is denied
// and is surfaced verbatim to the model.
type Result struct {
	Allow   bool
	Reason  string
	Sandbox *SandboxSpec
}

func allowed() Result { return Result{Allow: true} }

func denied(format string, args ...any) Result {
	return Result{Reason: "Permission denied: " + fmt.Sprintf(format, args...)}
}

// Engine decides every tool call against the policy, session grants, and
// mode defaults, escalating to the Asker when no rule settles it.
type Engine struct {
	policy *Policy
	asker  Asker
	cwd    string
	grants grantStore
}

// NewEngine builds an engine rooted at cwd. asker must not be nil.
func NewEngine(policy *Policy, asker Asker, cwd string) *Engine {
	return &Engine{policy: policy, asker: asker, cwd: cwd}
}

// Mode returns the active permission mode.
func (e *Engine) Mode() Mode { return e.policy.Mode }

// SetMode switches the permission stance mid-session (/mode command).
func (e *Engine) SetMode(m Mode) { e.policy.Mode = m }

// Grants lists the session grants for the /permissions UI.
func (e *Engine) Grants() []string { return e.grants.list() }

// Decide runs the decision pipeline for one tool call:
// explicit rules (flags above settings, deny above allow) → baseline
// screens → session grants → mode defaults → ask.
func (e *Engine) Decide(ctx context.Context, call ai.ToolCall) Result {
	info := describeCall(call, e.cwd)

	if r := firstMatch(e.policy.FlagDeny, info); r != nil {
		return denied("blocked by --deny rule %s", r)
	}
	if r := firstMatch(e.policy.FlagAllow, info); r != nil {
		return allowed()
	}
	if r := firstMatch(e.policy.Deny, info); r != nil {
		return denied("blocked by deny rule %s", r)
	}

	// Baseline screens (every mode except yolo). Dangerous bash patterns are
	// hard denials; suspicious paths force an ask that even an allow rule
	// cannot pre-approve.
	forcedAsk := ""
	if e.policy.Mode != ModeYolo {
		if info.Tool == "bash" {
			for _, re := range e.policy.DenyPatterns {
				if re.MatchString(info.Command) {
					return denied("command matches dangerous pattern %q", re)
				}
			}
		}
		switch {
		case info.Category == CatWrite && info.Path != "" && outsideCwd(info.Path, e.cwd):
			forcedAsk = fmt.Sprintf("%s outside the working directory %s", info.Tool, e.cwd)
		// grep is screened too: it returns file contents just like read.
		// A grep over a directory that merely contains a secret file is not
		// caught — output-level filtering is v2 territory.
		case (info.Tool == "read" || info.Tool == "grep") && info.Path != "" && matchesSecretGlob(info.Path, e.cwd):
			forcedAsk = "reading a file that may contain secrets"
		}
	}

	if forcedAsk == "" {
		if firstMatch(e.policy.Allow, info) != nil || e.grants.match(info) {
			return allowed()
		}
		switch e.defaultFor(info.Category) {
		case decAllow:
			return allowed()
		case decDeny:
			return denied("%s tools are not allowed in %s mode", info.Category, e.policy.Mode)
		}
		forcedAsk = fmt.Sprintf("%s requires approval in %s mode", info.Tool, e.policy.Mode)
	}
	return e.ask(ctx, call, info, forcedAsk)
}

// decision is a mode-default outcome.
type decision int

const (
	decAsk decision = iota
	decAllow
	decDeny
)

// defaultFor implements the mode × category default matrix.
func (e *Engine) defaultFor(c Category) decision {
	switch e.policy.Mode {
	case ModeYolo:
		return decAllow
	case ModeAutoEdit:
		if c == CatRead || c == CatWrite {
			return decAllow
		}
		return decAsk
	case ModePlan:
		if c == CatRead {
			return decAllow
		}
		return decDeny
	default: // prompt
		if c == CatRead {
			return decAllow
		}
		return decAsk
	}
}

// ask escalates to the Asker with a preview; an "always" answer records a
// generalized session grant before allowing.
func (e *Engine) ask(ctx context.Context, call ai.ToolCall, info CallInfo, why string) Result {
	req := AskRequest{Tool: call.Name, Args: call.Args, Preview: e.preview(call, info), Why: why}
	dec, err := e.asker.Ask(ctx, req)
	if err != nil {
		return denied("approval failed: %v", err)
	}
	switch dec {
	case AskAlways:
		e.grants.add(generalize(info))
		return allowed()
	case AskAllow:
		return allowed()
	}
	return denied("%s; approval was not granted (non-interactive sessions deny by default — re-run with --permission-mode yolo, or add an allow rule / --allow flag)", why)
}
