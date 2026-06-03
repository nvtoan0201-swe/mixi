// Package ai defines the normalized type system shared by all providers.
// It must not import any other internal/ package.
package ai

import "encoding/json"

// ── Content blocks (sealed union) ───────────────────────────────────────────

// Content is a sealed union: TextContent | ImageContent | ThinkingContent | ToolCall.
// The marker method prevents foreign implementations; json.go handles the
// "type" discriminator on (un)marshal.
type Content interface{ isContent() }

// TextContent is plain assistant/user text.
type TextContent struct {
	Text string `json:"text"`
	// TextSignature carries OpenAI Responses text-item identity (opaque JSON
	// string, e.g. {"v":1,"id":"...","phase":"final_answer"}). Replayed verbatim.
	TextSignature string `json:"textSignature,omitempty"`
}

// ImageContent is a base64-encoded image.
type ImageContent struct {
	Data     string `json:"data"`     // base64, no data: prefix
	MimeType string `json:"mimeType"` // "image/png" | "image/jpeg" | "image/gif" | "image/webp"
}

// ThinkingContent is an extended-thinking block.
type ThinkingContent struct {
	Thinking string `json:"thinking"`
	// Signature is the provider's opaque crypto payload (Anthropic signature_delta
	// accumulation, or serialized OpenAI reasoning item). Preserve byte-exact.
	Signature string `json:"signature,omitempty"`
	// Redacted marks Anthropic redacted_thinking; Signature then holds the
	// encrypted data payload and Thinking holds a placeholder.
	Redacted bool `json:"redacted,omitempty"`
}

// ToolCall is a model-issued tool invocation.
type ToolCall struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Args is the raw JSON object; during streaming it is the best-effort
	// repaired parse of the partial JSON, finalized at toolcall_end.
	Args json.RawMessage `json:"args"`
	// ThoughtSignature: provider-specific reasoning linkage (OpenRouter
	// reasoning_details, Google thought signatures). Opaque, replayed verbatim.
	ThoughtSignature string `json:"thoughtSignature,omitempty"`
}

func (TextContent) isContent()     {}
func (ImageContent) isContent()    {}
func (ThinkingContent) isContent() {}
func (ToolCall) isContent()        {}

// ── Messages (sealed union) ─────────────────────────────────────────────────

type Role string

const (
	RoleUser       Role = "user"
	RoleAssistant  Role = "assistant"
	RoleToolResult Role = "toolResult"
)

// Message is a sealed union: UserMessage | AssistantMessage | ToolResultMessage.
// json.go dispatches on the "role" discriminator.
type Message interface {
	isMessage()
	MsgRole() Role
	MsgTimestamp() int64 // unix ms
}

type UserMessage struct {
	Content   []Content `json:"content"` // TextContent | ImageContent only
	Timestamp int64     `json:"timestamp"`
}

type AssistantMessage struct {
	Content []Content `json:"content"` // TextContent | ThinkingContent | ToolCall
	// API is the wire API used, e.g. "anthropic-messages", "openai-completions".
	API string `json:"api"`
	// Provider is the logical vendor key, e.g. "anthropic", "openai", "openrouter".
	Provider string `json:"provider"`
	Model    string `json:"model"` // model requested
	// ResponseModel is the model that actually answered (router services may differ).
	ResponseModel string     `json:"responseModel,omitempty"`
	ResponseID    string     `json:"responseId,omitempty"`
	Usage         Usage      `json:"usage"`
	StopReason    StopReason `json:"stopReason"`
	ErrorMessage  string     `json:"errorMessage,omitempty"` // set when StopReason == error|aborted
	Timestamp     int64      `json:"timestamp"`
}

type ToolResultMessage struct {
	ToolCallID string    `json:"toolCallId"`
	ToolName   string    `json:"toolName"`
	Content    []Content `json:"content"` // TextContent | ImageContent
	// Details carries tool-specific structured metadata for UI/extensions
	// (e.g. edit diff). NEVER sent to the LLM. Concrete type per tool,
	// serialized as raw JSON for session persistence.
	Details   json.RawMessage `json:"details,omitempty"`
	IsError   bool            `json:"isError"`
	Timestamp int64           `json:"timestamp"`
}

func (UserMessage) isMessage()       {}
func (AssistantMessage) isMessage()  {}
func (ToolResultMessage) isMessage() {}

func (UserMessage) MsgRole() Role       { return RoleUser }
func (AssistantMessage) MsgRole() Role  { return RoleAssistant }
func (ToolResultMessage) MsgRole() Role { return RoleToolResult }

func (m UserMessage) MsgTimestamp() int64       { return m.Timestamp }
func (m AssistantMessage) MsgTimestamp() int64  { return m.Timestamp }
func (m ToolResultMessage) MsgTimestamp() int64 { return m.Timestamp }

// ── Enums ───────────────────────────────────────────────────────────────────

type StopReason string

const (
	StopReasonStop    StopReason = "stop"    // natural end_turn
	StopReasonLength  StopReason = "length"  // max_tokens hit
	StopReasonToolUse StopReason = "toolUse" // model requested tools
	StopReasonError   StopReason = "error"   // API/safety error (refusal maps here)
	StopReasonAborted StopReason = "aborted" // ctx cancelled
)

type ThinkingLevel string

const (
	ThinkingOff    ThinkingLevel = "off"
	ThinkingLow    ThinkingLevel = "low"
	ThinkingMedium ThinkingLevel = "medium"
	ThinkingHigh   ThinkingLevel = "high"
)

type CacheRetention string

const (
	CacheNone  CacheRetention = "none"
	CacheShort CacheRetention = "short" // default ephemeral (5m Anthropic)
	CacheLong  CacheRetention = "long"  // ttl:"1h" Anthropic / 24h OpenAI Responses
)

// ── Usage & cost ────────────────────────────────────────────────────────────

type Usage struct {
	Input      int `json:"input"`      // non-cached input tokens
	Output     int `json:"output"`     // output tokens (includes thinking)
	CacheRead  int `json:"cacheRead"`  // tokens served from prompt cache
	CacheWrite int `json:"cacheWrite"` // tokens newly written to cache
	// Total = Input+Output+CacheRead+CacheWrite, computed at stream finalize
	// (Anthropic does not send it).
	Total int  `json:"total"`
	Cost  Cost `json:"cost"`
}

type Cost struct { // USD
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cacheRead"`
	CacheWrite float64 `json:"cacheWrite"`
	Total      float64 `json:"total"`
}

// ── Model catalog ───────────────────────────────────────────────────────────

type Model struct {
	API           string `json:"api"`      // registry key: "anthropic-messages" | "openai-completions"
	Provider      string `json:"provider"` // "anthropic" | "openai" | "openrouter" ...
	ID            string `json:"id"`       // wire model id, e.g. "claude-sonnet-4-6"
	DisplayName   string `json:"displayName"`
	ContextWindow int    `json:"contextWindow"`
	MaxOutput     int    `json:"maxOutput"`
	BaseURL       string `json:"baseUrl,omitempty"` // override for proxies/routers
	Caps          Caps   `json:"caps"`
	Pricing       Price  `json:"pricing"`
}

type Caps struct {
	Vision        bool `json:"vision"`
	Thinking      bool `json:"thinking"`
	PromptCaching bool `json:"promptCaching"`
	LongCacheTTL  bool `json:"longCacheTtl"`
	ParallelTools bool `json:"parallelTools"`
}

type Price struct { // USD per million tokens
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cacheRead"`
	CacheWrite float64 `json:"cacheWrite"`
}

// ── Request context & options ───────────────────────────────────────────────

// ToolDef is the provider-facing tool definition.
type ToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Schema      json.RawMessage `json:"schema"` // JSON Schema object for parameters
}

// Context is one LLM request's content.
type Context struct {
	SystemPrompt string    `json:"systemPrompt,omitempty"`
	Messages     []Message `json:"messages"`
	Tools        []ToolDef `json:"tools,omitempty"`
}

// StreamOptions tunes a single streaming request.
type StreamOptions struct {
	APIKey         string         // resolved per-call (rotation-friendly)
	MaxTokens      int            // 0 → model.MaxOutput
	Temperature    *float64       // nil → provider default
	Thinking       ThinkingLevel  // off → no thinking block requested
	ThinkingBudget int            // tokens; 0 → level-derived default (low 2048, medium 8192, high 16384)
	CacheRetention CacheRetention // cache_control ttl selection
	SessionID      string         // prompt-cache affinity key (clamped to 64 chars for OpenAI)
	MaxRetries     int            // transport-level retries inside provider (default 2)
}
