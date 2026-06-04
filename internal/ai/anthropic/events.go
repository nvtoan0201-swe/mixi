package anthropic

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/user/mixi-agent/internal/ai"
)

// wireEvent is the superset of Anthropic SSE payload shapes, selected by Type.
type wireEvent struct {
	Type  string `json:"type"`
	Index int    `json:"index"`

	Message *struct {
		ID    string     `json:"id"`
		Model string     `json:"model"`
		Usage *wireUsage `json:"usage"`
	} `json:"message"`

	ContentBlock *struct {
		Type string `json:"type"`
		ID   string `json:"id"`
		Name string `json:"name"`
		Data string `json:"data"` // redacted_thinking payload
	} `json:"content_block"`

	Delta *struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		Thinking    string `json:"thinking"`
		PartialJSON string `json:"partial_json"`
		Signature   string `json:"signature"`
		StopReason  string `json:"stop_reason"`
	} `json:"delta"`

	Usage *wireUsage `json:"usage"`

	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// wireUsage uses pointers so absent/null fields never clobber earlier values
// (message_delta omits input_tokens, for example).
type wireUsage struct {
	InputTokens              *int `json:"input_tokens"`
	OutputTokens             *int `json:"output_tokens"`
	CacheReadInputTokens     *int `json:"cache_read_input_tokens"`
	CacheCreationInputTokens *int `json:"cache_creation_input_tokens"`
}

// block accumulates one in-flight content block, keyed by the API's index.
type block struct {
	kind      string // "text" | "thinking" | "redacted_thinking" | "tool_use" | "" (ignored)
	idx       int    // position in msg.Content
	text      strings.Builder
	signature strings.Builder
	jsonBuf   strings.Builder
}

// tracker folds the SSE event sequence into the partial AssistantMessage and
// the normalized StreamEvent sequence.
type tracker struct {
	model      ai.Model
	msg        ai.AssistantMessage
	blocks     map[int]*block
	stopReason string
}

func newTracker(model ai.Model) *tracker {
	return &tracker{
		model:  model,
		blocks: map[int]*block{},
		msg: ai.AssistantMessage{
			API:       apiName,
			Provider:  model.Provider,
			Model:     model.ID,
			Timestamp: time.Now().UnixMilli(),
		},
	}
}

// snapshot copies the message so emitted events stay immutable as the
// tracker keeps mutating Content in place.
func (t *tracker) snapshot() ai.AssistantMessage {
	m := t.msg
	m.Content = slices.Clone(t.msg.Content)
	return m
}

// fail finalizes the partial message as a terminal error event.
func (t *tracker) fail(reason ai.StopReason, msg string) ai.EventError {
	t.finalizeUsage()
	t.msg.StopReason = reason
	t.msg.ErrorMessage = msg
	return ai.EventError{Reason: reason, Message: t.snapshot()}
}

// handle processes one SSE data payload and returns the events to emit.
func (t *tracker) handle(data string) ([]ai.StreamEvent, error) {
	var ev wireEvent
	if err := json.Unmarshal([]byte(data), &ev); err != nil {
		return nil, fmt.Errorf("anthropic: bad SSE payload: %w", err)
	}
	switch ev.Type {
	case "message_start":
		if ev.Message != nil {
			t.msg.ResponseID = ev.Message.ID
			t.msg.ResponseModel = ev.Message.Model
			t.mergeUsage(ev.Message.Usage)
		}
		return []ai.StreamEvent{ai.EventStart{Partial: t.snapshot()}}, nil
	case "content_block_start":
		return t.startBlock(ev)
	case "content_block_delta":
		return t.deltaBlock(ev)
	case "content_block_stop":
		return t.stopBlock(ev.Index)
	case "message_delta":
		if ev.Delta != nil && ev.Delta.StopReason != "" {
			t.stopReason = ev.Delta.StopReason
		}
		t.mergeUsage(ev.Usage)
		return nil, nil
	case "message_stop":
		return t.finish()
	case "error":
		msg := "unknown error"
		if ev.Error != nil {
			msg = fmt.Sprintf("%s: %s", ev.Error.Type, ev.Error.Message)
		}
		return []ai.StreamEvent{t.fail(ai.StopReasonError, msg)}, nil
	default: // ping or future event types — ignore
		return nil, nil
	}
}

func (t *tracker) finish() ([]ai.StreamEvent, error) {
	t.finalizeUsage()
	reason := mapStopReason(t.stopReason)
	if reason == ai.StopReasonError {
		return []ai.StreamEvent{t.fail(reason, "model refused to answer ("+t.stopReason+")")}, nil
	}
	t.msg.StopReason = reason
	return []ai.StreamEvent{ai.EventDone{Reason: reason, Message: t.snapshot()}}, nil
}

// mergeUsage folds non-null wire fields into the running usage totals.
func (t *tracker) mergeUsage(u *wireUsage) {
	if u == nil {
		return
	}
	if u.InputTokens != nil {
		t.msg.Usage.Input = *u.InputTokens
	}
	if u.OutputTokens != nil {
		t.msg.Usage.Output = *u.OutputTokens
	}
	if u.CacheReadInputTokens != nil {
		t.msg.Usage.CacheRead = *u.CacheReadInputTokens
	}
	if u.CacheCreationInputTokens != nil {
		t.msg.Usage.CacheWrite = *u.CacheCreationInputTokens
	}
}

// finalizeUsage computes Total (the API never sends it) and the USD cost
// from the model's per-million pricing.
func (t *tracker) finalizeUsage() {
	u := &t.msg.Usage
	u.Total = u.Input + u.Output + u.CacheRead + u.CacheWrite
	p := t.model.Pricing
	u.Cost = ai.Cost{
		Input:      float64(u.Input) * p.Input / 1e6,
		Output:     float64(u.Output) * p.Output / 1e6,
		CacheRead:  float64(u.CacheRead) * p.CacheRead / 1e6,
		CacheWrite: float64(u.CacheWrite) * p.CacheWrite / 1e6,
	}
	u.Cost.Total = u.Cost.Input + u.Cost.Output + u.Cost.CacheRead + u.Cost.CacheWrite
}

// mapStopReason normalizes the wire stop_reason vocabulary.
func mapStopReason(s string) ai.StopReason {
	switch s {
	case "end_turn", "pause_turn", "stop_sequence", "":
		return ai.StopReasonStop
	case "max_tokens":
		return ai.StopReasonLength
	case "tool_use":
		return ai.StopReasonToolUse
	case "refusal", "sensitive":
		return ai.StopReasonError
	default:
		return ai.StopReasonStop
	}
}
