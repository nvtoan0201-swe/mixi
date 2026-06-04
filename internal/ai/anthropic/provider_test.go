package anthropic

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"go.uber.org/goleak"

	"github.com/user/mixi-agent/internal/ai"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

func newTestProvider(t *testing.T) *Provider {
	t.Helper()
	tr := &http.Transport{}
	t.Cleanup(tr.CloseIdleConnections)
	return &Provider{HTTPClient: &http.Client{Transport: tr}}
}

func serveFixture(t *testing.T, name string) *httptest.Server {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write(data)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func testModel(baseURL string) ai.Model {
	m, ok := ai.Lookup("anthropic", "claude-sonnet-4-6")
	if !ok {
		panic("catalog model missing")
	}
	m.BaseURL = baseURL
	return m
}

func collect(t *testing.T, ch <-chan ai.StreamEvent) []ai.StreamEvent {
	t.Helper()
	var out []ai.StreamEvent
	for ev := range ch {
		out = append(out, ev)
	}
	if len(out) == 0 {
		t.Fatal("stream produced no events")
	}
	return out
}

func eventKinds(events []ai.StreamEvent) []string {
	kinds := make([]string, len(events))
	for i, ev := range events {
		switch ev.(type) {
		case ai.EventStart:
			kinds[i] = "start"
		case ai.EventTextStart:
			kinds[i] = "text_start"
		case ai.EventTextDelta:
			kinds[i] = "text_delta"
		case ai.EventTextEnd:
			kinds[i] = "text_end"
		case ai.EventThinkingStart:
			kinds[i] = "thinking_start"
		case ai.EventThinkingDelta:
			kinds[i] = "thinking_delta"
		case ai.EventThinkingEnd:
			kinds[i] = "thinking_end"
		case ai.EventToolCallStart:
			kinds[i] = "toolcall_start"
		case ai.EventToolCallDelta:
			kinds[i] = "toolcall_delta"
		case ai.EventToolCallEnd:
			kinds[i] = "toolcall_end"
		case ai.EventDone:
			kinds[i] = "done"
		case ai.EventError:
			kinds[i] = "error"
		}
	}
	return kinds
}

// wantUsage mirrors the tracker's finalize arithmetic so expected messages
// compare bit-exact on cost floats.
func wantUsage(m ai.Model, in, out, cacheRead, cacheWrite int) ai.Usage {
	u := ai.Usage{Input: in, Output: out, CacheRead: cacheRead, CacheWrite: cacheWrite}
	u.Total = in + out + cacheRead + cacheWrite
	u.Cost = ai.Cost{
		Input:      float64(in) * m.Pricing.Input / 1e6,
		Output:     float64(out) * m.Pricing.Output / 1e6,
		CacheRead:  float64(cacheRead) * m.Pricing.CacheRead / 1e6,
		CacheWrite: float64(cacheWrite) * m.Pricing.CacheWrite / 1e6,
	}
	u.Cost.Total = u.Cost.Input + u.Cost.Output + u.Cost.CacheRead + u.Cost.CacheWrite
	return u
}

func runFixture(t *testing.T, fixture string) (ai.Model, []ai.StreamEvent) {
	t.Helper()
	srv := serveFixture(t, fixture)
	model := testModel(srv.URL)
	p := newTestProvider(t)
	ctx := ai.Context{Messages: []ai.Message{ai.UserMessage{Content: []ai.Content{ai.TextContent{Text: "hi"}}}}}
	events := collect(t, p.Stream(context.Background(), model, ctx, ai.StreamOptions{APIKey: "test"}))
	return model, events
}

func finalMessage(t *testing.T, events []ai.StreamEvent) ai.AssistantMessage {
	t.Helper()
	switch last := events[len(events)-1].(type) {
	case ai.EventDone:
		return last.Message
	case ai.EventError:
		return last.Message
	default:
		t.Fatalf("stream did not terminate with Done/Error, got %T", last)
		return ai.AssistantMessage{}
	}
}

func assertMessage(t *testing.T, got, want ai.AssistantMessage) {
	t.Helper()
	if got.Timestamp == 0 {
		t.Error("final message timestamp not set")
	}
	got.Timestamp = 0
	if !reflect.DeepEqual(got, want) {
		t.Errorf("final message mismatch\ngot:  %+v\nwant: %+v", got, want)
	}
}

func baseWant(model ai.Model, fixtureID string) ai.AssistantMessage {
	return ai.AssistantMessage{
		API:           apiName,
		Provider:      "anthropic",
		Model:         model.ID,
		ResponseModel: "claude-sonnet-4-6",
		ResponseID:    fixtureID,
	}
}

func TestGoldenTextOnly(t *testing.T) {
	model, events := runFixture(t, "text_only.sse")
	wantKinds := []string{"start", "text_start", "text_delta", "text_delta", "text_end", "done"}
	if got := eventKinds(events); !reflect.DeepEqual(got, wantKinds) {
		t.Fatalf("event sequence = %v, want %v", got, wantKinds)
	}
	want := baseWant(model, "msg_text01")
	want.Content = []ai.Content{ai.TextContent{Text: "Hello, world!"}}
	want.Usage = wantUsage(model, 25, 12, 0, 0)
	want.StopReason = ai.StopReasonStop
	assertMessage(t, finalMessage(t, events), want)
}

func TestGoldenThinkingSignature(t *testing.T) {
	model, events := runFixture(t, "thinking_signature.sse")
	wantKinds := []string{
		"start", "thinking_start", "thinking_delta", "thinking_delta", "thinking_end",
		"text_start", "text_delta", "text_end", "done",
	}
	if got := eventKinds(events); !reflect.DeepEqual(got, wantKinds) {
		t.Fatalf("event sequence = %v, want %v", got, wantKinds)
	}
	want := baseWant(model, "msg_think01")
	want.Content = []ai.Content{
		ai.ThinkingContent{Thinking: "Let me reason about this.", Signature: "EpYDCkYIBxgCKkD7+vIn8tqA=="},
		ai.TextContent{Text: "The answer is 42."},
	}
	want.Usage = wantUsage(model, 40, 58, 0, 0)
	want.StopReason = ai.StopReasonStop
	assertMessage(t, finalMessage(t, events), want)
}

func TestGoldenRedactedThinking(t *testing.T) {
	model, events := runFixture(t, "redacted_thinking.sse")
	final := finalMessage(t, events)
	want := baseWant(model, "msg_redact01")
	want.Content = []ai.Content{
		ai.ThinkingContent{
			Thinking:  "[Reasoning redacted]",
			Signature: "EmwKAhgBEgy3vUmRhKjFTcSXi0saDP3GW6vIErn9yc4HQCIw3evGW0NDxqFROGGSGRKLG40nXAVRTzCl/wDmJpmIPCSyVKLZGYwvENC4tNUKZviIKh3WUgWaGryt4Cqewz3xkSayY9eM2QqXSc8WdQ==",
			Redacted:  true,
		},
		ai.TextContent{Text: "I cannot share my reasoning."},
	}
	want.Usage = wantUsage(model, 30, 20, 0, 0)
	want.StopReason = ai.StopReasonStop
	assertMessage(t, final, want)
}

func TestGoldenParallelTools(t *testing.T) {
	model, events := runFixture(t, "parallel_tools.sse")
	wantKinds := []string{
		"start", "text_start", "text_delta", "text_end",
		"toolcall_start", "toolcall_delta", "toolcall_delta", "toolcall_end",
		"toolcall_start", "toolcall_delta", "toolcall_delta", "toolcall_end", "done",
	}
	if got := eventKinds(events); !reflect.DeepEqual(got, wantKinds) {
		t.Fatalf("event sequence = %v, want %v", got, wantKinds)
	}
	done := events[len(events)-1].(ai.EventDone)
	if done.Reason != ai.StopReasonToolUse {
		t.Errorf("done reason = %s, want toolUse", done.Reason)
	}
	want := baseWant(model, "msg_tools01")
	want.Content = []ai.Content{
		ai.TextContent{Text: "Running both lookups."},
		ai.ToolCall{ID: "toolu_01A", Name: "grep", Args: []byte(`{"pattern":"func main","path":"."}`)},
		ai.ToolCall{ID: "toolu_01B", Name: "read", Args: []byte(`{"path":"go.mod"}`)},
	}
	want.Usage = wantUsage(model, 120, 95, 0, 0)
	want.StopReason = ai.StopReasonToolUse
	assertMessage(t, done.Message, want)
}

func TestGoldenMidStreamError(t *testing.T) {
	model, events := runFixture(t, "mid_stream_error.sse")
	errEv, ok := events[len(events)-1].(ai.EventError)
	if !ok {
		t.Fatalf("last event = %T, want EventError", events[len(events)-1])
	}
	if errEv.Reason != ai.StopReasonError {
		t.Errorf("reason = %s, want error", errEv.Reason)
	}
	want := baseWant(model, "msg_err01")
	want.Content = []ai.Content{ai.TextContent{Text: "Partial answ"}} // partial preserved
	want.Usage = wantUsage(model, 15, 1, 0, 0)
	want.StopReason = ai.StopReasonError
	want.ErrorMessage = "overloaded_error: Overloaded"
	assertMessage(t, errEv.Message, want)
}

func TestGoldenUsageWithCache(t *testing.T) {
	model, events := runFixture(t, "usage_cache.sse")
	final := finalMessage(t, events)
	want := baseWant(model, "msg_cache01")
	want.Content = []ai.Content{ai.TextContent{Text: "Cached reply."}}
	want.Usage = wantUsage(model, 210, 7, 4096, 2048)
	want.StopReason = ai.StopReasonStop
	assertMessage(t, final, want)
}

func TestAbortMidStreamPreservesPartial(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		_, _ = io.WriteString(w, `event: message_start
data: {"type":"message_start","message":{"id":"msg_abort","model":"claude-sonnet-4-6","usage":{"input_tokens":10,"output_tokens":1}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text"}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Partial"}}

`)
		fl.Flush()
		<-r.Context().Done() // hold the stream open until the client aborts
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := newTestProvider(t)
	ch := p.Stream(ctx, testModel(srv.URL), ai.Context{
		Messages: []ai.Message{ai.UserMessage{Content: []ai.Content{ai.TextContent{Text: "hi"}}}},
	}, ai.StreamOptions{APIKey: "test"})

	var events []ai.StreamEvent
	for ev := range ch {
		events = append(events, ev)
		if _, ok := ev.(ai.EventTextDelta); ok {
			cancel()
		}
	}
	errEv, ok := events[len(events)-1].(ai.EventError)
	if !ok {
		t.Fatalf("last event = %T, want EventError", events[len(events)-1])
	}
	if errEv.Reason != ai.StopReasonAborted {
		t.Errorf("reason = %s, want aborted", errEv.Reason)
	}
	if errEv.Message.StopReason != ai.StopReasonAborted {
		t.Errorf("message stop reason = %s, want aborted", errEv.Message.StopReason)
	}
	if !reflect.DeepEqual(errEv.Message.Content, []ai.Content{ai.TextContent{Text: "Partial"}}) {
		t.Errorf("partial content not preserved: %+v", errEv.Message.Content)
	}
}

func TestRetryOn429ThenSuccess(t *testing.T) {
	fixture, err := os.ReadFile(filepath.Join("testdata", "text_only.sse"))
	if err != nil {
		t.Fatal(err)
	}
	var requests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		if requests <= 2 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprint(w, `{"error":{"type":"rate_limit_error"}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write(fixture)
	}))
	t.Cleanup(srv.Close)

	p := newTestProvider(t)
	events := collect(t, p.Stream(context.Background(), testModel(srv.URL), ai.Context{
		Messages: []ai.Message{ai.UserMessage{Content: []ai.Content{ai.TextContent{Text: "hi"}}}},
	}, ai.StreamOptions{APIKey: "test"}))
	if _, ok := events[len(events)-1].(ai.EventDone); !ok {
		t.Fatalf("expected Done after retries, got %T", events[len(events)-1])
	}
	if requests != 3 {
		t.Errorf("requests = %d, want 3 (2 retries)", requests)
	}
}

func TestRetriesExhaustedOn500(t *testing.T) {
	var requests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	p := newTestProvider(t)
	events := collect(t, p.Stream(context.Background(), testModel(srv.URL), ai.Context{
		Messages: []ai.Message{ai.UserMessage{Content: []ai.Content{ai.TextContent{Text: "hi"}}}},
	}, ai.StreamOptions{APIKey: "test", MaxRetries: 1}))
	errEv, ok := events[len(events)-1].(ai.EventError)
	if !ok {
		t.Fatalf("expected EventError, got %T", events[len(events)-1])
	}
	if errEv.Reason != ai.StopReasonError {
		t.Errorf("reason = %s, want error", errEv.Reason)
	}
	if requests != 2 {
		t.Errorf("requests = %d, want 2 (1 retry)", requests)
	}
}

func TestNoRetryOn400(t *testing.T) {
	var requests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":{"type":"invalid_request_error","message":"bad"}}`)
	}))
	t.Cleanup(srv.Close)

	p := newTestProvider(t)
	events := collect(t, p.Stream(context.Background(), testModel(srv.URL), ai.Context{
		Messages: []ai.Message{ai.UserMessage{Content: []ai.Content{ai.TextContent{Text: "hi"}}}},
	}, ai.StreamOptions{APIKey: "test"}))
	if _, ok := events[len(events)-1].(ai.EventError); !ok {
		t.Fatalf("expected EventError, got %T", events[len(events)-1])
	}
	if requests != 1 {
		t.Errorf("requests = %d, want 1 (no retry on 4xx)", requests)
	}
}

func TestProviderRegistered(t *testing.T) {
	p, err := ai.Resolve(apiName)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := p.(*Provider); !ok {
		t.Fatalf("registered provider is %T, want *anthropic.Provider", p)
	}
}
