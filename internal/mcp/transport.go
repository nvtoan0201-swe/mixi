package mcp

import (
	"context"
	"encoding/json"
)

// Transport moves raw JSON-RPC messages to and from one MCP server. Stdio
// is the only implementation today; HTTP/SSE would slot in here.
type Transport interface {
	// Send writes one JSON-RPC message. Safe for concurrent use.
	Send(ctx context.Context, msg json.RawMessage) error
	// Receive returns the next message; io.EOF on orderly close, other
	// errors on crash. Single consumer.
	Receive(ctx context.Context) (json.RawMessage, error)
	// Close terminates the transport (kills the subprocess group for stdio).
	Close() error
}
