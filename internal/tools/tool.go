// Package tools defines the Tool interface implemented by the nine built-in
// tools, the MCP adapter, and the extension adapter, plus a name-keyed
// registry. Result/Update types are shared with the agent dispatcher.
package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/user/mixi-agent/internal/ai"
)

// ExecMode declares a tool's batching semantics: a batch containing any
// ExecSequential tool runs all calls one at a time, in source order.
type ExecMode int

const (
	ExecParallel ExecMode = iota
	ExecSequential
)

// ToolResult is what a tool returns. The dispatcher wraps it into an
// ai.ToolResultMessage; Details is UI/extension metadata, never sent to
// the LLM.
type ToolResult struct {
	Content []ai.Content // TextContent | ImageContent
	Details json.RawMessage
	IsError bool
}

// Text builds a plain-text success result.
func Text(s string) ToolResult {
	return ToolResult{Content: []ai.Content{ai.TextContent{Text: s}}}
}

// Errorf builds an IsError result with a formatted message.
func Errorf(format string, args ...any) ToolResult {
	return ToolResult{
		Content: []ai.Content{ai.TextContent{Text: fmt.Sprintf(format, args...)}},
		IsError: true,
	}
}

// ToolUpdate is a best-effort partial-output snapshot streamed by
// long-running tools (bash). Content is the accumulated output so far.
type ToolUpdate struct {
	Content string
}

// Tool is implemented by built-ins, MCP-adapted tools, and extension tools.
type Tool interface {
	Name() string
	Description() string
	Schema() json.RawMessage // JSON Schema for arguments
	// Mode declares execution semantics for batching.
	Mode() ExecMode
	// Execute runs the call. updates is non-nil for streaming tools;
	// implementations send best-effort partial output and must not block on
	// it (send via select+default). Returning error ⇒ IsError tool result
	// with err.Error() as content. Panics are recovered by the dispatcher.
	Execute(ctx context.Context, args json.RawMessage, updates chan<- ToolUpdate) (ToolResult, error)
}

// Registry is a name-keyed tool set. Not safe for concurrent mutation;
// build it once at startup, read freely afterwards.
type Registry struct {
	byName map[string]Tool
	order  []Tool
}

func NewRegistry() *Registry {
	return &Registry{byName: map[string]Tool{}}
}

// Register adds a tool, rejecting duplicate names.
func (r *Registry) Register(t Tool) error {
	name := t.Name()
	if _, dup := r.byName[name]; dup {
		return fmt.Errorf("tools: duplicate tool name %q", name)
	}
	r.byName[name] = t
	r.order = append(r.order, t)
	return nil
}

// Get returns the tool registered under name.
func (r *Registry) Get(name string) (Tool, bool) {
	t, ok := r.byName[name]
	return t, ok
}

// All returns tools in registration order.
func (r *Registry) All() []Tool {
	return append([]Tool(nil), r.order...)
}

// Defs projects the registry into provider-facing tool definitions.
func (r *Registry) Defs() []ai.ToolDef {
	defs := make([]ai.ToolDef, 0, len(r.order))
	for _, t := range r.order {
		defs = append(defs, ai.ToolDef{Name: t.Name(), Description: t.Description(), Schema: t.Schema()})
	}
	return defs
}
