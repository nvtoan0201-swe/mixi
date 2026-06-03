package ai

import (
	"context"
	"strings"
	"testing"
)

func registerScripted(t *testing.T, api string, events []StreamEvent) Model {
	t.Helper()
	Register(fakeProvider{api: api, events: func(Model) []StreamEvent { return events }})
	return Model{API: api, Provider: "test", ID: "scripted"}
}

func TestStreamUnknownAPI(t *testing.T) {
	if _, err := Stream(context.Background(), Model{API: "test-missing"}, Context{}, StreamOptions{}); err == nil {
		t.Error("Stream with unregistered API: want error")
	}
}

func TestCompleteDrainsToDone(t *testing.T) {
	final := AssistantMessage{
		Content:    []Content{TextContent{Text: "done"}},
		StopReason: StopReasonStop,
	}
	model := registerScripted(t, "test-complete-done", []StreamEvent{
		EventStart{},
		EventTextStart{ContentIndex: 0},
		EventTextDelta{ContentIndex: 0, Delta: "do"},
		EventTextDelta{ContentIndex: 0, Delta: "ne"},
		EventTextEnd{ContentIndex: 0, Text: "done"},
		EventDone{Reason: StopReasonStop, Message: final},
	})
	got, err := Complete(context.Background(), model, Context{}, StreamOptions{})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(got.Content) != 1 || got.StopReason != StopReasonStop {
		t.Errorf("final message = %#v, want %#v", got, final)
	}
}

func TestCompleteReturnsErrorEvent(t *testing.T) {
	partial := AssistantMessage{
		Content:      []Content{TextContent{Text: "par"}},
		StopReason:   StopReasonError,
		ErrorMessage: "overloaded",
	}
	model := registerScripted(t, "test-complete-error", []StreamEvent{
		EventStart{},
		EventError{Reason: StopReasonError, Message: partial},
	})
	got, err := Complete(context.Background(), model, Context{}, StreamOptions{})
	if err == nil {
		t.Fatal("Complete after EventError: want error")
	}
	if !strings.Contains(err.Error(), "overloaded") {
		t.Errorf("error %q does not surface ErrorMessage", err)
	}
	if len(got.Content) != 1 {
		t.Errorf("partial content lost: %#v", got)
	}
}

func TestCompleteChannelClosedWithoutTerminal(t *testing.T) {
	model := registerScripted(t, "test-complete-truncated", []StreamEvent{
		EventStart{},
		EventTextDelta{ContentIndex: 0, Delta: "x"},
	})
	if _, err := Complete(context.Background(), model, Context{}, StreamOptions{}); err == nil {
		t.Error("stream closed without Done/Error: want error")
	}
}
