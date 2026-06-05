// Package ext implements the subprocess extension host: extensions are
// executables speaking newline-delimited JSON over stdio (shared wire
// framing with MCP), crash-isolated by the OS, with host-enforced timeouts
// so a hung extension can never stall the agent loop.
package ext

import (
	"encoding/json"
	"fmt"
)

// ProtocolVersion is the extension wire protocol revision. Experimental
// until v1 sign-off; a hello with a different version is rejected.
const ProtocolVersion = 1

// Message type discriminators. Every message is one JSON document per line
// shaped {"id"?, "type", ...}.
const (
	TypeHello          = "hello"           // ext→host
	TypeReady          = "ready"           // host→ext
	TypeEvent          = "event"           // host→ext
	TypeEventResponse  = "event_response"  // ext→host (blocking events only)
	TypeToolCall       = "tool_call"       // host→ext (extension-registered tools)
	TypeToolResult     = "tool_result"     // ext→host
	TypeAction         = "action"          // ext→host
	TypeActionResponse = "action_response" // host→ext
	TypeShutdown       = "shutdown"        // host→ext
)

// envelope is the common header peeked off every inbound line before the
// full per-type decode.
type envelope struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

// ToolDef is one extension-registered tool announced in hello.
type ToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Schema      json.RawMessage `json:"schema"`
}

// CommandDef is one extension-registered slash command announced in hello.
type CommandDef struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// Hello is the extension's opening message; it must arrive within the
// handshake deadline or the process is killed.
type Hello struct {
	Type      string       `json:"type"`
	Name      string       `json:"name"`
	Version   string       `json:"version"`
	Protocol  int          `json:"protocol"`
	Subscribe []string     `json:"subscribe,omitempty"` // event names; "*" = all
	Tools     []ToolDef    `json:"tools,omitempty"`
	Commands  []CommandDef `json:"commands,omitempty"`
}

// Ready acknowledges a validated hello and carries session context.
type Ready struct {
	Type      string `json:"type"`
	SessionID string `json:"sessionId"`
	Cwd       string `json:"cwd"`
	Mode      string `json:"mode"`
	Model     string `json:"model"`
}

// EventMsg delivers one subscribed event. ExpectsResponse is true only for
// blocking events (tool_call); everything else is fire-and-forget.
type EventMsg struct {
	Type            string          `json:"type"`
	ID              int64           `json:"id"`
	Event           string          `json:"event"`
	Payload         json.RawMessage `json:"payload,omitempty"`
	ExpectsResponse bool            `json:"expectsResponse,omitempty"`
}

// BlockInfo vetoes a gated tool call; Reason reaches the model verbatim.
type BlockInfo struct {
	Reason string `json:"reason"`
}

// MutateInfo rewrites a gated tool call's arguments.
type MutateInfo struct {
	Args json.RawMessage `json:"args"`
}

// EventResponse answers a blocking event. Nil Block and Mutate = no-op.
type EventResponse struct {
	Type   string      `json:"type"`
	ID     int64       `json:"id"`
	Block  *BlockInfo  `json:"block,omitempty"`
	Mutate *MutateInfo `json:"mutate,omitempty"`
}

// ToolCallMsg dispatches a call of an extension-registered tool.
type ToolCallMsg struct {
	Type string          `json:"type"`
	ID   int64           `json:"id"`
	Tool string          `json:"tool"`
	Args json.RawMessage `json:"args,omitempty"`
}

// ContentItem is one tool-result content block (text or base64 image).
type ContentItem struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	Data     string `json:"data,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
}

// ToolResultMsg answers a ToolCallMsg with the same ID.
type ToolResultMsg struct {
	Type    string        `json:"type"`
	ID      int64         `json:"id"`
	Content []ContentItem `json:"content"`
	IsError bool          `json:"isError,omitempty"`
}

// ActionMsg is an extension-initiated request against the host API.
type ActionMsg struct {
	Type   string          `json:"type"`
	ID     int64           `json:"id"`
	Action string          `json:"action"` // send_message | set_status | append_entry | notify | ask_select | register_command (register_tool is hello-only)
	Params json.RawMessage `json:"params,omitempty"`
}

// ActionResponse answers an ActionMsg with the same ID; Error is set when
// the action was rejected.
type ActionResponse struct {
	Type   string          `json:"type"`
	ID     int64           `json:"id"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

// Shutdown asks the extension to exit; the host SIGKILLs the process group
// after a grace period.
type Shutdown struct {
	Type   string `json:"type"`
	Reason string `json:"reason"`
}

// decodeEnvelope peeks the id/type header off a raw line.
func decodeEnvelope(line []byte) (envelope, error) {
	var env envelope
	if err := json.Unmarshal(line, &env); err != nil {
		return env, fmt.Errorf("ext: malformed message: %w", err)
	}
	if env.Type == "" {
		return env, fmt.Errorf("ext: message missing type")
	}
	return env, nil
}
