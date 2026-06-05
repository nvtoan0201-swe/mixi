// Package perm is the permission engine: every tool call is decided against
// explicit rules, session grants, and mode defaults before it executes.
package perm

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// Mode is the session-wide permission stance.
type Mode string

const (
	ModePlan     Mode = "plan"
	ModePrompt   Mode = "prompt"
	ModeAutoEdit Mode = "auto-edit"
	ModeYolo     Mode = "yolo"
)

// ParseMode validates a mode string; empty defaults to prompt.
func ParseMode(s string) (Mode, error) {
	switch Mode(s) {
	case ModePlan, ModePrompt, ModeAutoEdit, ModeYolo:
		return Mode(s), nil
	case "":
		return ModePrompt, nil
	}
	return "", fmt.Errorf("perm: unknown mode %q (valid: plan|prompt|auto-edit|yolo)", s)
}

// Category buckets tools by the kind of access they grant.
type Category string

const (
	CatRead    Category = "read"
	CatWrite   Category = "write"
	CatExecute Category = "execute"
	CatMCP     Category = "mcp"
)

// CategoryOf maps a tool name to its access category. Unknown tools are
// treated as execute — the most restricted bucket — so a new tool can never
// slip through more loosely than bash would.
func CategoryOf(tool string) Category {
	switch tool {
	case "read", "grep", "find", "ls", "bash_output":
		return CatRead
	case "write", "edit":
		return CatWrite
	case "bash", "kill_bash":
		return CatExecute
	}
	if strings.HasPrefix(tool, "mcp__") {
		return CatMCP
	}
	return CatExecute
}

// Rule is one parsed allow/deny entry. Tool is the rule's target ("bash",
// "read", "write", "edit") or "" for an mcp__ name glob held in Spec.
type Rule struct {
	Tool string
	Spec string
	raw  string
}

func (r Rule) String() string { return r.raw }

// ruleSyntax matches `tool(spec)` for the four spec-bearing tools.
var ruleSyntax = regexp.MustCompile(`^(bash|read|write|edit)\((.+)\)$`)

// ParseRule parses one rule string. The grammar is frozen: bash(prefix*)
// command glob, read/write/edit(glob) path glob, mcp__server__tool name
// exact-or-glob. source names the settings file or flag for error context.
func ParseRule(raw, source string) (Rule, error) {
	if strings.HasPrefix(raw, "mcp__") {
		return Rule{Spec: raw, raw: raw}, nil
	}
	m := ruleSyntax.FindStringSubmatch(raw)
	if m == nil {
		return Rule{}, fmt.Errorf("perm: %s: bad rule %q (want tool(spec) with tool in bash|read|write|edit, or an mcp__server__tool glob)", source, raw)
	}
	if m[1] != "bash" {
		// Path globs are validated eagerly so a typo surfaces at startup,
		// not as a silently never-matching rule.
		if !doublestar.ValidatePattern(m[2]) {
			return Rule{}, fmt.Errorf("perm: %s: bad glob in rule %q", source, raw)
		}
	}
	return Rule{Tool: m[1], Spec: m[2], raw: raw}, nil
}

// ParseRules parses a rule list, reporting the first error with its source.
func ParseRules(raws []string, source string) ([]Rule, error) {
	rules := make([]Rule, 0, len(raws))
	for _, raw := range raws {
		r, err := ParseRule(raw, source)
		if err != nil {
			return nil, err
		}
		rules = append(rules, r)
	}
	return rules, nil
}

// Matches reports whether the rule covers the call. Path rules apply to all
// three file tools interchangeably only when tools match exactly; a read
// rule never covers a write.
func (r Rule) Matches(c CallInfo) bool {
	if r.Tool == "" { // mcp name glob
		return c.Category == CatMCP && wildcardMatch(r.Spec, c.Tool)
	}
	if r.Tool != c.Tool {
		return false
	}
	if r.Spec == "" {
		// Spec-less rules exist only as session grants for tools without a
		// command or path (e.g. kill_bash): match every call of the tool.
		return true
	}
	if r.Tool == "bash" {
		return wildcardMatch(r.Spec, c.Command)
	}
	return pathGlobMatch(r.Spec, c.Path, c.Cwd)
}

// Policy is the merged rule set the engine consults. Flag rules outrank
// config rules; within a layer deny beats allow.
type Policy struct {
	Mode         Mode
	FlagAllow    []Rule
	FlagDeny     []Rule
	Allow        []Rule
	Deny         []Rule
	DenyPatterns []*regexp.Regexp
}

// PolicyConfig carries the raw strings a Policy is built from.
type PolicyConfig struct {
	Mode         string
	Allow        []string
	Deny         []string
	DenyPatterns []string
	FlagAllow    []string // --allow values, highest precedence
	FlagDeny     []string // --deny values, highest precedence
}

// NewPolicy parses and validates the full rule set.
func NewPolicy(c PolicyConfig) (*Policy, error) {
	mode, err := ParseMode(c.Mode)
	if err != nil {
		return nil, err
	}
	p := &Policy{Mode: mode}
	if p.Allow, err = ParseRules(c.Allow, "settings allow"); err != nil {
		return nil, err
	}
	if p.Deny, err = ParseRules(c.Deny, "settings deny"); err != nil {
		return nil, err
	}
	if p.FlagAllow, err = ParseRules(c.FlagAllow, "--allow"); err != nil {
		return nil, err
	}
	if p.FlagDeny, err = ParseRules(c.FlagDeny, "--deny"); err != nil {
		return nil, err
	}
	for _, pat := range c.DenyPatterns {
		re, err := regexp.Compile(pat)
		if err != nil {
			return nil, fmt.Errorf("perm: settings denyPatterns: bad regex %q: %w", pat, err)
		}
		p.DenyPatterns = append(p.DenyPatterns, re)
	}
	return p, nil
}

// firstMatch returns the first rule covering the call, or nil.
func firstMatch(rules []Rule, c CallInfo) *Rule {
	for i := range rules {
		if rules[i].Matches(c) {
			return &rules[i]
		}
	}
	return nil
}
