// Package mcp implements a Model Context Protocol client: tools over stdio,
// with server lifecycle management, restart backoff, and adaptation of MCP
// tools into the agent's tool registry.
package mcp

import (
	"encoding/json"
	"fmt"
)

// ProtocolVersion is the MCP revision this client speaks first.
const ProtocolVersion = "2025-06-18"

// FallbackProtocolVersion is accepted when a server negotiates down.
const FallbackProtocolVersion = "2025-03-26"

// Request is a JSON-RPC 2.0 call expecting a response.
type Request struct {
	JSONRPC string          `json:"jsonrpc"` // always "2.0"
	ID      int64           `json:"id"`      // monotonic per client
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Response carries either Result or Error for the Request with the same ID.
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// Notification is a call without an id; no response is expected.
type Notification struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// RPCError is the JSON-RPC error object.
type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *RPCError) Error() string {
	return fmt.Sprintf("rpc error %d: %s", e.Code, e.Message)
}

// initializeParams is the client half of the MCP handshake.
type initializeParams struct {
	ProtocolVersion string         `json:"protocolVersion"`
	Capabilities    map[string]any `json:"capabilities"`
	ClientInfo      implInfo       `json:"clientInfo"`
}

type implInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// initializeResult is the server half of the handshake.
type initializeResult struct {
	ProtocolVersion string         `json:"protocolVersion"`
	Capabilities    map[string]any `json:"capabilities"`
	ServerInfo      implInfo       `json:"serverInfo"`
}

// ToolInfo is one entry of a tools/list result.
type ToolInfo struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

type listToolsResult struct {
	Tools []ToolInfo `json:"tools"`
}

type callToolParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

// callToolResult is a tools/call result: a content list plus error flag.
type callToolResult struct {
	Content []contentItem `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

// contentItem is one MCP content block. Type discriminates; unknown types
// are surfaced to the LLM as JSON-stringified text rather than dropped.
type contentItem struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Data     string `json:"data,omitempty"`     // base64 for images
	MimeType string `json:"mimeType,omitempty"` // for images
	raw      json.RawMessage
}

// UnmarshalJSON keeps the raw bytes so unknown content types can be passed
// through verbatim as text.
func (c *contentItem) UnmarshalJSON(b []byte) error {
	type plain contentItem
	var p plain
	if err := json.Unmarshal(b, &p); err != nil {
		return err
	}
	*c = contentItem(p)
	c.raw = append(json.RawMessage(nil), b...)
	return nil
}
