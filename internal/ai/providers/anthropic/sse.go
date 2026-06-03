package anthropic

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/user/mixi-agent/internal/ai"
)

// ── SSE stream state ──────────────────────────────────────────────────────────

type streamState struct {
	partial    ai.AssistantMessage
	blockType  map[int]string // index → "text" | "thinking" | "tool_use"
	blockAccum map[int]string // index → accumulated text / thinking / tool args
	toolID     map[int]string // index → tool call ID
	toolName   map[int]string // index → tool call name
}

func newStreamState(modelName, apiName string) *streamState {
	return &streamState{
		partial:    ai.AssistantMessage{Role: "assistant", Model: modelName, API: apiName},
		blockType:  make(map[int]string),
		blockAccum: make(map[int]string),
		toolID:     make(map[int]string),
		toolName:   make(map[int]string),
	}
}

// ── Anthropic SSE payload shapes ──────────────────────────────────────────────

type sseMessageStart struct {
	Message struct {
		Usage struct {
			InputTokens              int `json:"input_tokens"`
			CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
			CacheReadInputTokens     int `json:"cache_read_input_tokens"`
		} `json:"usage"`
	} `json:"message"`
}

type sseContentBlockStart struct {
	Index        int `json:"index"`
	ContentBlock struct {
		Type string `json:"type"` // "text" | "thinking" | "tool_use"
		ID   string `json:"id,omitempty"`
		Name string `json:"name,omitempty"`
	} `json:"content_block"`
}

type sseContentBlockDelta struct {
	Index int `json:"index"`
	Delta struct {
		Type        string `json:"type"` // "text_delta" | "thinking_delta" | "input_json_delta"
		Text        string `json:"text,omitempty"`
		Thinking    string `json:"thinking,omitempty"`
		PartialJSON string `json:"partial_json,omitempty"`
	} `json:"delta"`
}

type sseContentBlockStop struct {
	Index int `json:"index"`
}

type sseMessageDelta struct {
	Delta struct {
		StopReason string `json:"stop_reason"`
	} `json:"delta"`
	Usage struct {
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

// ── Main stream function ──────────────────────────────────────────────────────

func (p *Provider) stream(ctx context.Context, model ai.Model, c ai.Context, opts ai.StreamOptions, ch chan<- ai.AssistantMessageEvent) error {
	apiReq, err := buildAPIRequest(model, c, opts)
	if err != nil {
		return err
	}

	httpReq, err := buildHTTPRequest(ctx, opts.APIKey, apiReq)
	if err != nil {
		return err
	}

	// Extended thinking requires a beta header
	if model.Thinking != ai.ThinkingOff {
		httpReq.Header.Set("anthropic-beta", "interleaved-thinking-2025-05-14")
	}

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("anthropic: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return readErrorBody(resp)
	}

	state := newStreamState(model.Name, "anthropic-messages")
	scanner := bufio.NewScanner(resp.Body)
	var eventType string

	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			eventType = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			data := strings.TrimPrefix(line, "data: ")
			if data == "[DONE]" {
				return nil
			}
			if err := dispatchSSEEvent(eventType, data, state, ch); err != nil {
				return err
			}
			eventType = ""
		}
	}
	return scanner.Err()
}

func readErrorBody(resp *http.Response) error {
	var body struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if body.Error.Message != "" {
		return fmt.Errorf("anthropic: HTTP %d: %s", resp.StatusCode, body.Error.Message)
	}
	return fmt.Errorf("anthropic: HTTP %d", resp.StatusCode)
}

// ── SSE event dispatch ────────────────────────────────────────────────────────

func dispatchSSEEvent(eventType, data string, state *streamState, ch chan<- ai.AssistantMessageEvent) error {
	switch eventType {
	case "message_start":
		return handleMessageStart(data, state, ch)
	case "content_block_start":
		return handleContentBlockStart(data, state, ch)
	case "content_block_delta":
		return handleContentBlockDelta(data, state, ch)
	case "content_block_stop":
		return handleContentBlockStop(data, state, ch)
	case "message_delta":
		return handleMessageDelta(data, state)
	case "message_stop":
		return handleMessageStop(state, ch)
	case "ping", "":
		// ignore
	}
	return nil
}

func handleMessageStart(data string, state *streamState, ch chan<- ai.AssistantMessageEvent) error {
	var e sseMessageStart
	if err := json.Unmarshal([]byte(data), &e); err != nil {
		return fmt.Errorf("anthropic: parse message_start: %w", err)
	}
	state.partial.Usage.InputTokens = e.Message.Usage.InputTokens
	state.partial.Usage.CacheWriteTokens = e.Message.Usage.CacheCreationInputTokens
	state.partial.Usage.CacheReadTokens = e.Message.Usage.CacheReadInputTokens
	ch <- ai.StartEvent{Partial: state.partial}
	return nil
}

func handleContentBlockStart(data string, state *streamState, ch chan<- ai.AssistantMessageEvent) error {
	var e sseContentBlockStart
	if err := json.Unmarshal([]byte(data), &e); err != nil {
		return fmt.Errorf("anthropic: parse content_block_start: %w", err)
	}
	state.blockType[e.Index] = e.ContentBlock.Type
	if e.ContentBlock.Type == "text" {
		ch <- ai.TextStartEvent{ContentIndex: e.Index}
	} else if e.ContentBlock.Type == "tool_use" {
		state.toolID[e.Index] = e.ContentBlock.ID
		state.toolName[e.Index] = e.ContentBlock.Name
	}
	return nil
}

func handleContentBlockDelta(data string, state *streamState, ch chan<- ai.AssistantMessageEvent) error {
	var e sseContentBlockDelta
	if err := json.Unmarshal([]byte(data), &e); err != nil {
		return fmt.Errorf("anthropic: parse content_block_delta: %w", err)
	}
	switch e.Delta.Type {
	case "text_delta":
		state.blockAccum[e.Index] += e.Delta.Text
		ch <- ai.TextDeltaEvent{ContentIndex: e.Index, Delta: e.Delta.Text}
	case "thinking_delta":
		state.blockAccum[e.Index] += e.Delta.Thinking
		ch <- ai.ThinkingDeltaEvent{ContentIndex: e.Index, Delta: e.Delta.Thinking}
	case "input_json_delta":
		state.blockAccum[e.Index] += e.Delta.PartialJSON
		ch <- ai.ToolCallDeltaEvent{
			ContentIndex: e.Index,
			ID:           state.toolID[e.Index],
			Name:         state.toolName[e.Index],
			ArgsDelta:    e.Delta.PartialJSON,
		}
	}
	return nil
}

func handleContentBlockStop(data string, state *streamState, ch chan<- ai.AssistantMessageEvent) error {
	var e sseContentBlockStop
	if err := json.Unmarshal([]byte(data), &e); err != nil {
		return fmt.Errorf("anthropic: parse content_block_stop: %w", err)
	}
	accum := state.blockAccum[e.Index]
	switch state.blockType[e.Index] {
	case "text":
		state.partial.Content = append(state.partial.Content, ai.TextContent{Type: "text", Text: accum})
		ch <- ai.TextEndEvent{ContentIndex: e.Index}
	case "thinking":
		state.partial.Content = append(state.partial.Content, ai.ThinkingContent{
			Type:     "thinking",
			Thinking: accum,
			// Signature is opaque; not available in streaming deltas
		})
	case "tool_use":
		args := json.RawMessage(accum)
		if len(args) == 0 {
			args = json.RawMessage("{}")
		}
		state.partial.Content = append(state.partial.Content, ai.ToolCall{
			ID:   state.toolID[e.Index],
			Name: state.toolName[e.Index],
			Args: args,
		})
	}
	return nil
}

func handleMessageDelta(data string, state *streamState) error {
	var e sseMessageDelta
	if err := json.Unmarshal([]byte(data), &e); err != nil {
		return fmt.Errorf("anthropic: parse message_delta: %w", err)
	}
	state.partial.Usage.OutputTokens = e.Usage.OutputTokens
	state.partial.StopReason = mapStopReason(e.Delta.StopReason)
	return nil
}

func handleMessageStop(state *streamState, ch chan<- ai.AssistantMessageEvent) error {
	if state.partial.StopReason == ai.StopReasonError {
		ch <- ai.ErrorEvent{Reason: ai.StopReasonError, Message: state.partial}
	} else {
		ch <- ai.DoneEvent{Reason: state.partial.StopReason, Message: state.partial}
	}
	return nil
}

func mapStopReason(reason string) ai.StopReason {
	switch reason {
	case "end_turn":
		return ai.StopReasonStop
	case "tool_use":
		return ai.StopReasonToolUse
	case "max_tokens":
		return ai.StopReasonLength
	default:
		return ai.StopReasonStop
	}
}
