// Package ai defines the shared type system and provider registry for all AI providers.
// This package must not import any other internal/ package.
package ai

import "encoding/json"

// ContentBlock is a union of TextContent | ImageContent | ThinkingContent | ToolCall.
type ContentBlock = any

// Message is a union of UserMessage | AssistantMessage | ToolResultMessage.
type Message = any

// ── Content blocks ────────────────────────────────────────────────────────────

type TextContent struct {
	Type string `json:"type"` // "text"
	Text string `json:"text"`
}

type ImageContent struct {
	Type      string `json:"type"`      // "image"
	MediaType string `json:"mediaType"` // "image/png", "image/jpeg", etc.
	Data      string `json:"data"`      // base64-encoded
}

type ThinkingContent struct {
	Type      string `json:"type"`      // "thinking"
	Thinking  string `json:"thinking"`
	Signature string `json:"signature"` // Anthropic redaction token; pass through opaque
}

type ToolCall struct {
	ID   string          `json:"id"`
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"` // JSON object matching tool parameters schema
}

// ── Messages ──────────────────────────────────────────────────────────────────

type UserMessage struct {
	Role      string         `json:"role"`      // "user"
	Content   []ContentBlock `json:"content"`   // TextContent | ImageContent
	Timestamp int64          `json:"timestamp"`
}

type AssistantMessage struct {
	Role       string         `json:"role"`                 // "assistant"
	Content    []ContentBlock `json:"content"`              // TextContent | ThinkingContent | ToolCall
	Model      string         `json:"model"`
	API        string         `json:"api"`                  // "anthropic-messages", "openai-chat"
	Usage      Usage          `json:"usage"`
	StopReason StopReason     `json:"stopReason"`
	ErrorMsg   string         `json:"errorMessage,omitempty"`
	Timestamp  int64          `json:"timestamp"`
}

type ToolResultMessage struct {
	Role       string         `json:"role"`              // "toolResult"
	ToolCallID string         `json:"toolCallId"`
	Content    []ContentBlock `json:"content"`           // TextContent | ImageContent
	Details    any            `json:"details,omitempty"` // structured tool-specific metadata
	IsError    bool           `json:"isError"`
	Timestamp  int64          `json:"timestamp"`
}

// ── Enums ─────────────────────────────────────────────────────────────────────

type StopReason string

const (
	StopReasonStop    StopReason = "stop"
	StopReasonLength  StopReason = "length"
	StopReasonToolUse StopReason = "toolUse"
	StopReasonError   StopReason = "error"
	StopReasonAborted StopReason = "aborted"
)

type ThinkingLevel string

const (
	ThinkingOff    ThinkingLevel = "off"
	ThinkingLow    ThinkingLevel = "low"
	ThinkingMedium ThinkingLevel = "medium"
	ThinkingHigh   ThinkingLevel = "high"
)

type ExecutionMode string

const (
	ExecutionSequential ExecutionMode = "sequential"
	ExecutionParallel   ExecutionMode = "parallel"
)

type QueueDrainMode string

const (
	QueueDrainOneAtATime QueueDrainMode = "one-at-a-time"
	QueueDrainAll        QueueDrainMode = "all"
)

// ── Usage & Model ──────────────────────────────────────────────────────────────

type Usage struct {
	InputTokens      int `json:"inputTokens"`
	OutputTokens     int `json:"outputTokens"`
	CacheReadTokens  int `json:"cacheReadTokens"`  // Anthropic prompt caching
	CacheWriteTokens int `json:"cacheWriteTokens"` // Anthropic prompt caching
}

type Model struct {
	API           string        `json:"api"`           // must match a registered Provider.API()
	Name          string        `json:"name"`          // e.g. "claude-sonnet-4-6"
	ContextWindow int           `json:"contextWindow"` // max input tokens
	MaxOutput     int           `json:"maxOutput"`     // max output tokens
	Thinking      ThinkingLevel `json:"thinking"`
	Capabilities  ModelCaps     `json:"capabilities"`
}

type ModelCaps struct {
	Vision   bool `json:"vision"`
	Thinking bool `json:"thinking"`
	Tools    bool `json:"tools"`
}

// ── Context & Tool ────────────────────────────────────────────────────────────

type Context struct {
	SystemPrompt string    `json:"systemPrompt,omitempty"`
	Messages     []Message `json:"messages"`
	Tools        []ToolDef `json:"tools,omitempty"`
}

type ToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"` // JSON Schema object
}

// ── Stream options ─────────────────────────────────────────────────────────────

type StreamOptions struct {
	Temperature      *float64 `json:"temperature,omitempty"`
	MaxTokens        *int     `json:"maxTokens,omitempty"`
	APIKey           string   `json:"-"` // never serialized; would leak credentials
	CacheBreakpoints []int    `json:"-"` // indices for cache_control injection
}

// ── Provider-space events ──────────────────────────────────────────────────────

// AssistantMessageEvent is the sealed interface for all streaming events emitted by a provider.
type AssistantMessageEvent interface{ isAssistantMessageEvent() }

type StartEvent struct{ Partial AssistantMessage }
type TextStartEvent struct{ ContentIndex int }
type TextDeltaEvent struct {
	ContentIndex int
	Delta        string
}
type TextEndEvent struct{ ContentIndex int }
type ThinkingDeltaEvent struct {
	ContentIndex int
	Delta        string
}
type ToolCallDeltaEvent struct {
	ContentIndex int
	ID, Name     string
	ArgsDelta    string
}
type DoneEvent struct {
	Reason  StopReason
	Message AssistantMessage
}
type ErrorEvent struct {
	Reason  StopReason
	Err     error
	Message AssistantMessage
}

func (StartEvent) isAssistantMessageEvent()         {}
func (TextStartEvent) isAssistantMessageEvent()     {}
func (TextDeltaEvent) isAssistantMessageEvent()     {}
func (TextEndEvent) isAssistantMessageEvent()       {}
func (ThinkingDeltaEvent) isAssistantMessageEvent() {}
func (ToolCallDeltaEvent) isAssistantMessageEvent() {}
func (DoneEvent) isAssistantMessageEvent()          {}
func (ErrorEvent) isAssistantMessageEvent()         {}
