package ai

import (
	"encoding/json"
	"fmt"
)

// Discriminator values injected/dispatched by the union (un)marshalers.
// They match Pi's on-disk field names so session files stay tooling-compatible.
const (
	typeText     = "text"
	typeImage    = "image"
	typeThinking = "thinking"
	typeToolCall = "toolCall"
)

// MarshalContent serializes a Content union member with an injected
// "type" discriminator field.
func MarshalContent(c Content) ([]byte, error) {
	switch v := c.(type) {
	case TextContent:
		return json.Marshal(struct {
			Type string `json:"type"`
			TextContent
		}{typeText, v})
	case ImageContent:
		return json.Marshal(struct {
			Type string `json:"type"`
			ImageContent
		}{typeImage, v})
	case ThinkingContent:
		return json.Marshal(struct {
			Type string `json:"type"`
			ThinkingContent
		}{typeThinking, v})
	case ToolCall:
		return json.Marshal(struct {
			Type string `json:"type"`
			ToolCall
		}{typeToolCall, v})
	default:
		return nil, fmt.Errorf("ai: cannot marshal unknown Content type %T", c)
	}
}

// UnmarshalContent deserializes a Content union member by dispatching on
// the "type" discriminator field.
func UnmarshalContent(data []byte) (Content, error) {
	var probe struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, fmt.Errorf("ai: invalid content JSON: %w", err)
	}
	switch probe.Type {
	case typeText:
		var v TextContent
		err := json.Unmarshal(data, &v)
		return v, err
	case typeImage:
		var v ImageContent
		err := json.Unmarshal(data, &v)
		return v, err
	case typeThinking:
		var v ThinkingContent
		err := json.Unmarshal(data, &v)
		return v, err
	case typeToolCall:
		var v ToolCall
		err := json.Unmarshal(data, &v)
		return v, err
	default:
		return nil, fmt.Errorf("ai: unknown content type %q", probe.Type)
	}
}

// contentSlice (un)marshals []Content with per-element discriminators so the
// message wire structs below can delegate to the union helpers.
type contentSlice []Content

func (s contentSlice) MarshalJSON() ([]byte, error) {
	raws := make([]json.RawMessage, len(s))
	for i, c := range s {
		b, err := MarshalContent(c)
		if err != nil {
			return nil, err
		}
		raws[i] = b
	}
	return json.Marshal(raws)
}

func (s *contentSlice) UnmarshalJSON(data []byte) error {
	var raws []json.RawMessage
	if err := json.Unmarshal(data, &raws); err != nil {
		return err
	}
	out := make([]Content, len(raws))
	for i, r := range raws {
		c, err := UnmarshalContent(r)
		if err != nil {
			return err
		}
		out[i] = c
	}
	*s = out
	return nil
}

// Wire shapes mirror the public message structs plus the "role" discriminator;
// Content swaps to contentSlice for nested union handling.
type userMessageJSON struct {
	Role      Role         `json:"role"`
	Content   contentSlice `json:"content"`
	Timestamp int64        `json:"timestamp"`
}

type assistantMessageJSON struct {
	Role          Role         `json:"role"`
	Content       contentSlice `json:"content"`
	API           string       `json:"api"`
	Provider      string       `json:"provider"`
	Model         string       `json:"model"`
	ResponseModel string       `json:"responseModel,omitempty"`
	ResponseID    string       `json:"responseId,omitempty"`
	Usage         Usage        `json:"usage"`
	StopReason    StopReason   `json:"stopReason"`
	ErrorMessage  string       `json:"errorMessage,omitempty"`
	Timestamp     int64        `json:"timestamp"`
}

type toolResultMessageJSON struct {
	Role       Role            `json:"role"`
	ToolCallID string          `json:"toolCallId"`
	ToolName   string          `json:"toolName"`
	Content    contentSlice    `json:"content"`
	Details    json.RawMessage `json:"details,omitempty"`
	IsError    bool            `json:"isError"`
	Timestamp  int64           `json:"timestamp"`
}

// MarshalMessage serializes a Message union member with an injected
// "role" discriminator field; nested Content gets "type" discriminators.
func MarshalMessage(m Message) ([]byte, error) {
	switch v := m.(type) {
	case UserMessage:
		return json.Marshal(userMessageJSON{RoleUser, v.Content, v.Timestamp})
	case AssistantMessage:
		return json.Marshal(assistantMessageJSON{
			RoleAssistant, v.Content, v.API, v.Provider, v.Model,
			v.ResponseModel, v.ResponseID, v.Usage, v.StopReason,
			v.ErrorMessage, v.Timestamp,
		})
	case ToolResultMessage:
		return json.Marshal(toolResultMessageJSON{
			RoleToolResult, v.ToolCallID, v.ToolName, v.Content,
			v.Details, v.IsError, v.Timestamp,
		})
	default:
		return nil, fmt.Errorf("ai: cannot marshal unknown Message type %T", m)
	}
}

// UnmarshalMessage deserializes a Message union member by dispatching on
// the "role" discriminator field.
func UnmarshalMessage(data []byte) (Message, error) {
	var probe struct {
		Role Role `json:"role"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, fmt.Errorf("ai: invalid message JSON: %w", err)
	}
	switch probe.Role {
	case RoleUser:
		var w userMessageJSON
		if err := json.Unmarshal(data, &w); err != nil {
			return nil, err
		}
		return UserMessage{Content: w.Content, Timestamp: w.Timestamp}, nil
	case RoleAssistant:
		var w assistantMessageJSON
		if err := json.Unmarshal(data, &w); err != nil {
			return nil, err
		}
		return AssistantMessage{
			Content: w.Content, API: w.API, Provider: w.Provider,
			Model: w.Model, ResponseModel: w.ResponseModel,
			ResponseID: w.ResponseID, Usage: w.Usage,
			StopReason: w.StopReason, ErrorMessage: w.ErrorMessage,
			Timestamp: w.Timestamp,
		}, nil
	case RoleToolResult:
		var w toolResultMessageJSON
		if err := json.Unmarshal(data, &w); err != nil {
			return nil, err
		}
		return ToolResultMessage{
			ToolCallID: w.ToolCallID, ToolName: w.ToolName,
			Content: w.Content, Details: w.Details,
			IsError: w.IsError, Timestamp: w.Timestamp,
		}, nil
	default:
		return nil, fmt.Errorf("ai: unknown message role %q", probe.Role)
	}
}
