package ai

// StreamEvent is the sealed union of provider streaming events.
// Order contract per stream:
//
//	EventStart, then ≥0 of ({Text,Thinking,ToolCall}{Start,Delta,End} grouped
//	by ContentIndex, indices strictly increasing), then exactly one of
//	EventDone | EventError. After Done/Error the channel is closed.
type StreamEvent interface{ isStreamEvent() }

// EventStart opens the stream; Partial holds the empty in-progress message.
type EventStart struct{ Partial AssistantMessage }

// EventTextStart begins text block ContentIndex.
type EventTextStart struct {
	ContentIndex int
	Partial      AssistantMessage
}

// EventTextDelta carries an incremental text fragment.
type EventTextDelta struct {
	ContentIndex int
	Delta        string
	Partial      AssistantMessage
}

// EventTextEnd closes text block ContentIndex with the full accumulated text.
type EventTextEnd struct {
	ContentIndex int
	Text         string // full accumulated text
	Partial      AssistantMessage
}

// EventThinkingStart begins thinking block ContentIndex.
type EventThinkingStart struct {
	ContentIndex int
	Partial      AssistantMessage
}

// EventThinkingDelta carries an incremental thinking fragment.
type EventThinkingDelta struct {
	ContentIndex int
	Delta        string
	Partial      AssistantMessage
}

// EventThinkingEnd closes thinking block ContentIndex with the full text.
type EventThinkingEnd struct {
	ContentIndex int
	Thinking     string
	Partial      AssistantMessage
}

// EventToolCallStart begins tool-call block ContentIndex.
type EventToolCallStart struct {
	ContentIndex int
	ID           string
	Name         string
	Partial      AssistantMessage
}

// EventToolCallDelta carries the raw partial-JSON fragment; Args on the
// Partial message's ToolCall is the best-effort repaired parse so far.
type EventToolCallDelta struct {
	ContentIndex int
	Delta        string
	Partial      AssistantMessage
}

// EventToolCallEnd closes tool-call block ContentIndex.
type EventToolCallEnd struct {
	ContentIndex int
	ToolCall     ToolCall // finalized: Args fully parsed
	Partial      AssistantMessage
}

// EventDone terminates a successful stream. Reason ∈ {stop, length, toolUse}.
type EventDone struct {
	Reason  StopReason
	Message AssistantMessage // final, Usage complete
}

// EventError terminates a failed/cancelled stream. Reason ∈ {error, aborted}.
// Message preserves all partial content received before failure.
type EventError struct {
	Reason  StopReason
	Message AssistantMessage // StopReason+ErrorMessage set
}

func (EventStart) isStreamEvent()         {}
func (EventTextStart) isStreamEvent()     {}
func (EventTextDelta) isStreamEvent()     {}
func (EventTextEnd) isStreamEvent()       {}
func (EventThinkingStart) isStreamEvent() {}
func (EventThinkingDelta) isStreamEvent() {}
func (EventThinkingEnd) isStreamEvent()   {}
func (EventToolCallStart) isStreamEvent() {}
func (EventToolCallDelta) isStreamEvent() {}
func (EventToolCallEnd) isStreamEvent()   {}
func (EventDone) isStreamEvent()          {}
func (EventError) isStreamEvent()         {}
