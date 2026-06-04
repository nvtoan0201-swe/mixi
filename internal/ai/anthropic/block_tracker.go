package anthropic

import (
	"encoding/json"
	"fmt"

	"github.com/user/mixi-agent/internal/ai"
	"github.com/user/mixi-agent/internal/ai/partialjson"
)

// Content-block lifecycle: content_block_start / _delta / _stop handling for
// the tracker, keyed by the API's block index.

func (t *tracker) startBlock(ev wireEvent) ([]ai.StreamEvent, error) {
	if ev.ContentBlock == nil {
		return nil, fmt.Errorf("anthropic: content_block_start without content_block")
	}
	b := &block{idx: len(t.msg.Content)}
	t.blocks[ev.Index] = b
	switch ev.ContentBlock.Type {
	case "text":
		b.kind = "text"
		t.msg.Content = append(t.msg.Content, ai.TextContent{})
		return []ai.StreamEvent{ai.EventTextStart{ContentIndex: b.idx, Partial: t.snapshot()}}, nil
	case "thinking":
		b.kind = "thinking"
		t.msg.Content = append(t.msg.Content, ai.ThinkingContent{})
		return []ai.StreamEvent{ai.EventThinkingStart{ContentIndex: b.idx, Partial: t.snapshot()}}, nil
	case "redacted_thinking":
		b.kind = "redacted_thinking"
		t.msg.Content = append(t.msg.Content, ai.ThinkingContent{
			Thinking:  "[Reasoning redacted]",
			Signature: ev.ContentBlock.Data,
			Redacted:  true,
		})
		return []ai.StreamEvent{ai.EventThinkingStart{ContentIndex: b.idx, Partial: t.snapshot()}}, nil
	case "tool_use":
		b.kind = "tool_use"
		t.msg.Content = append(t.msg.Content, ai.ToolCall{
			ID: ev.ContentBlock.ID, Name: ev.ContentBlock.Name, Args: json.RawMessage("{}"),
		})
		return []ai.StreamEvent{ai.EventToolCallStart{
			ContentIndex: b.idx, ID: ev.ContentBlock.ID, Name: ev.ContentBlock.Name, Partial: t.snapshot(),
		}}, nil
	default: // unknown block type: track silently so its deltas don't error
		b.kind = ""
		b.idx = -1
		return nil, nil
	}
}

func (t *tracker) deltaBlock(ev wireEvent) ([]ai.StreamEvent, error) {
	b := t.blocks[ev.Index]
	if b == nil || ev.Delta == nil || b.kind == "" {
		return nil, nil
	}
	switch ev.Delta.Type {
	case "text_delta":
		b.text.WriteString(ev.Delta.Text)
		t.msg.Content[b.idx] = ai.TextContent{Text: b.text.String()}
		return []ai.StreamEvent{ai.EventTextDelta{ContentIndex: b.idx, Delta: ev.Delta.Text, Partial: t.snapshot()}}, nil
	case "thinking_delta":
		b.text.WriteString(ev.Delta.Thinking)
		t.msg.Content[b.idx] = t.thinkingAt(b)
		return []ai.StreamEvent{ai.EventThinkingDelta{ContentIndex: b.idx, Delta: ev.Delta.Thinking, Partial: t.snapshot()}}, nil
	case "signature_delta":
		// Opaque crypto payload — accumulate verbatim; no dedicated event.
		b.signature.WriteString(ev.Delta.Signature)
		t.msg.Content[b.idx] = t.thinkingAt(b)
		return nil, nil
	case "input_json_delta":
		b.jsonBuf.WriteString(ev.Delta.PartialJSON)
		tc := t.msg.Content[b.idx].(ai.ToolCall)
		tc.Args = partialjson.ParsePartial([]byte(b.jsonBuf.String()))
		t.msg.Content[b.idx] = tc
		return []ai.StreamEvent{ai.EventToolCallDelta{ContentIndex: b.idx, Delta: ev.Delta.PartialJSON, Partial: t.snapshot()}}, nil
	default:
		return nil, nil
	}
}

// thinkingAt rebuilds the ThinkingContent at b from its accumulators,
// preserving redaction state.
func (t *tracker) thinkingAt(b *block) ai.ThinkingContent {
	cur, _ := t.msg.Content[b.idx].(ai.ThinkingContent)
	if b.text.Len() > 0 {
		cur.Thinking = b.text.String()
	}
	if b.signature.Len() > 0 {
		cur.Signature = b.signature.String()
	}
	return cur
}

func (t *tracker) stopBlock(index int) ([]ai.StreamEvent, error) {
	b := t.blocks[index]
	if b == nil || b.kind == "" {
		return nil, nil
	}
	delete(t.blocks, index)
	switch b.kind {
	case "text":
		return []ai.StreamEvent{ai.EventTextEnd{ContentIndex: b.idx, Text: b.text.String(), Partial: t.snapshot()}}, nil
	case "thinking", "redacted_thinking":
		think := t.msg.Content[b.idx].(ai.ThinkingContent)
		return []ai.StreamEvent{ai.EventThinkingEnd{ContentIndex: b.idx, Thinking: think.Thinking, Partial: t.snapshot()}}, nil
	case "tool_use":
		tc := t.msg.Content[b.idx].(ai.ToolCall)
		tc.Args = partialjson.ParsePartial([]byte(b.jsonBuf.String()))
		t.msg.Content[b.idx] = tc
		return []ai.StreamEvent{ai.EventToolCallEnd{ContentIndex: b.idx, ToolCall: tc, Partial: t.snapshot()}}, nil
	}
	return nil, nil
}
