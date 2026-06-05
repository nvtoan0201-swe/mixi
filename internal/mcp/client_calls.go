package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/user/mixi-agent/internal/ai"
)

// Protocol-level operations of Client: handshake, tool listing, tool calls,
// and the mapping of MCP content blocks onto agent content. The connection
// core (id routing, timeouts, crash handling) lives in client.go.

// notifyServer sends a fire-and-forget notification.
func (c *Client) notifyServer(ctx context.Context, method string) error {
	b, err := json.Marshal(Notification{JSONRPC: "2.0", Method: method})
	if err != nil {
		return err
	}
	return c.tr.Send(ctx, b)
}

// Initialize runs the MCP handshake: initialize request (pinned protocol
// revision, fallback accepted) followed by the initialized notification.
func (c *Client) Initialize(ctx context.Context) error {
	res, err := c.call(ctx, "initialize", initializeParams{
		ProtocolVersion: ProtocolVersion,
		Capabilities:    map[string]any{},
		ClientInfo:      implInfo{Name: "mixi", Version: "1"},
	})
	if err != nil {
		return err
	}
	var ir initializeResult
	if err := json.Unmarshal(res, &ir); err != nil {
		return fmt.Errorf("mcp %s: initialize result: %w", c.name, err)
	}
	switch ir.ProtocolVersion {
	case ProtocolVersion, FallbackProtocolVersion:
		c.protocolVersion = ir.ProtocolVersion
	default:
		return fmt.Errorf("mcp %s: unsupported protocol version %q (want %s or %s)",
			c.name, ir.ProtocolVersion, ProtocolVersion, FallbackProtocolVersion)
	}
	return c.notifyServer(ctx, "notifications/initialized")
}

// NegotiatedVersion returns the protocol revision agreed at handshake.
func (c *Client) NegotiatedVersion() string { return c.protocolVersion }

// ListTools fetches the server's tool catalog.
func (c *Client) ListTools(ctx context.Context) ([]ToolInfo, error) {
	res, err := c.call(ctx, "tools/list", struct{}{})
	if err != nil {
		return nil, err
	}
	var lr listToolsResult
	if err := json.Unmarshal(res, &lr); err != nil {
		return nil, fmt.Errorf("mcp %s: tools/list result: %w", c.name, err)
	}
	return lr.Tools, nil
}

// CallTool invokes one tool and maps its content to agent content blocks.
func (c *Client) CallTool(ctx context.Context, name string, args json.RawMessage) ([]ai.Content, bool, error) {
	res, err := c.call(ctx, "tools/call", callToolParams{Name: name, Arguments: args})
	if err != nil {
		return nil, false, err
	}
	var cr callToolResult
	if err := json.Unmarshal(res, &cr); err != nil {
		return nil, false, fmt.Errorf("mcp %s: tools/call result: %w", c.name, err)
	}
	return c.mapContent(cr.Content), cr.IsError, nil
}

// mapContent converts MCP content blocks: text and image natively, every
// other type JSON-stringified into text (with a WARN) so nothing the server
// said is silently dropped.
func (c *Client) mapContent(items []contentItem) []ai.Content {
	out := make([]ai.Content, 0, len(items))
	for _, it := range items {
		switch it.Type {
		case "text":
			out = append(out, ai.TextContent{Text: it.Text})
		case "image":
			out = append(out, ai.ImageContent{Data: it.Data, MimeType: it.MimeType})
		default:
			c.log.Warn("mcp: passing unknown content type through as text",
				"server", c.name, "type", it.Type)
			out = append(out, ai.TextContent{Text: string(it.raw)})
		}
	}
	return out
}
