package ext

import (
	"context"
	"encoding/json"
	"time"

	"github.com/user/mixi-agent/internal/ai"
	"github.com/user/mixi-agent/internal/tools"
)

// toolCallTimeout bounds one extension tool execution.
const toolCallTimeout = 30 * time.Second

// permissiveSchema accepts any object; used when an extension declares no
// schema (the extension validates its own arguments).
var permissiveSchema = json.RawMessage(`{"type":"object"}`)

// extTool projects one extension-registered tool into the agent's registry
// under its raw name.
type extTool struct {
	host *Host
	ext  *extension
	def  ToolDef
}

func (t *extTool) Name() string        { return t.def.Name }
func (t *extTool) Description() string { return "[ext:" + t.ext.name + "] " + t.def.Description }

func (t *extTool) Schema() json.RawMessage {
	if len(t.def.Schema) == 0 {
		return permissiveSchema
	}
	return t.def.Schema
}

// Mode is parallel: extensions own their concurrency, and one slow
// extension tool must not serialize unrelated built-in calls.
func (t *extTool) Mode() tools.ExecMode { return tools.ExecParallel }

func (t *extTool) Execute(ctx context.Context, args json.RawMessage, _ chan<- tools.ToolUpdate) (tools.ToolResult, error) {
	e := t.ext
	if st := e.getState(); st != StateReady && st != StateDegraded {
		return tools.Errorf("Extension %q is %s; the tool is unavailable.", e.name, st), nil
	}
	e.mu.Lock()
	p := e.proc
	e.mu.Unlock()
	if p == nil {
		return tools.Errorf("Extension %q is not running; the tool is unavailable.", e.name), nil
	}
	id, ch := e.register()
	if err := p.send(ToolCallMsg{Type: TypeToolCall, ID: id, Tool: t.def.Name, Args: args}); err != nil {
		e.unregister(id)
		return tools.Errorf("Extension %q could not be reached: %v", e.name, err), nil
	}
	timer := time.NewTimer(toolCallTimeout)
	defer timer.Stop()
	select {
	case r := <-ch:
		if r.err != nil {
			return tools.Errorf("Extension %q crashed during the call. It is restarting; retry the tool if needed.", e.name), nil
		}
		if r.toolRes == nil {
			return tools.Errorf("Extension %q sent a malformed tool result.", e.name), nil
		}
		return tools.ToolResult{Content: contentToAI(r.toolRes.Content), IsError: r.toolRes.IsError}, nil
	case <-timer.C:
		e.unregister(id)
		return tools.Errorf("Extension %q did not answer the tool call within %s.", e.name, toolCallTimeout), nil
	case <-ctx.Done():
		e.unregister(id)
		return tools.ToolResult{}, ctx.Err()
	}
}

// contentToAI maps wire content blocks onto provider content. Unknown
// types surface as JSON text rather than being dropped.
func contentToAI(items []ContentItem) []ai.Content {
	if len(items) == 0 {
		return []ai.Content{ai.TextContent{Text: ""}}
	}
	out := make([]ai.Content, 0, len(items))
	for _, it := range items {
		switch it.Type {
		case "text":
			out = append(out, ai.TextContent{Text: it.Text})
		case "image":
			out = append(out, ai.ImageContent{Data: it.Data, MimeType: it.MimeType})
		default:
			b, _ := json.Marshal(it)
			out = append(out, ai.TextContent{Text: string(b)})
		}
	}
	return out
}
