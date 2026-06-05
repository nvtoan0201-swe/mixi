package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/user/mixi-agent/internal/ai"
	"github.com/user/mixi-agent/internal/wire"
)

// chanTransport is an in-process Transport: Send lands in `toServer`,
// Receive drains `toClient`. Close signals EOF (crash) via `closed` so
// concurrent injects never race a channel close.
type chanTransport struct {
	toServer  chan json.RawMessage
	toClient  chan json.RawMessage
	closed    chan struct{}
	closeOnce sync.Once
}

func newChanTransport() *chanTransport {
	return &chanTransport{
		toServer: make(chan json.RawMessage, 64),
		toClient: make(chan json.RawMessage, 64),
		closed:   make(chan struct{}),
	}
}

func (t *chanTransport) Send(ctx context.Context, msg json.RawMessage) error {
	select {
	case <-t.closed:
		return io.ErrClosedPipe
	case t.toServer <- msg:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (t *chanTransport) Receive(ctx context.Context) (json.RawMessage, error) {
	select {
	case <-t.closed:
		return nil, io.EOF
	default:
	}
	select {
	case <-t.closed:
		return nil, io.EOF
	case m := <-t.toClient:
		return m, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// inject delivers a server→client message, dropping it if closed.
func (t *chanTransport) inject(msg json.RawMessage) {
	select {
	case <-t.closed:
	case t.toClient <- msg:
	}
}

func (t *chanTransport) Close() error {
	t.closeOnce.Do(func() { close(t.closed) })
	return nil
}

// serve answers requests from the client with handler-provided results
// until the transport closes. Notifications from the client are recorded.
func (t *chanTransport) serve(tb testing.TB, notifs *[]string, mu *sync.Mutex,
	handler func(method string, req Request) (any, *RPCError)) {
	tb.Helper()
	go func() {
		for raw := range t.toServer {
			var req Request
			if err := json.Unmarshal(raw, &req); err != nil {
				continue
			}
			if req.Method != "" && !json.Valid(raw) {
				continue
			}
			var hasID struct {
				ID *int64 `json:"id"`
			}
			json.Unmarshal(raw, &hasID)
			if hasID.ID == nil { // notification from client
				if notifs != nil {
					mu.Lock()
					*notifs = append(*notifs, req.Method)
					mu.Unlock()
				}
				continue
			}
			result, rpcErr := handler(req.Method, req)
			resp := Response{JSONRPC: "2.0", ID: req.ID}
			if rpcErr != nil {
				resp.Error = rpcErr
			} else {
				b, _ := json.Marshal(result)
				resp.Result = b
			}
			out, _ := json.Marshal(resp)
			t.inject(out)
		}
	}()
}

// stdHandler answers initialize/tools/list/tools/call with canned data.
func stdHandler(version string) func(string, Request) (any, *RPCError) {
	return func(method string, req Request) (any, *RPCError) {
		switch method {
		case "initialize":
			return initializeResult{ProtocolVersion: version,
				ServerInfo: implInfo{Name: "fake", Version: "0"}}, nil
		case "tools/list":
			return listToolsResult{Tools: []ToolInfo{{
				Name: "echo", Description: "echoes",
				InputSchema: json.RawMessage(`{"type":"object"}`),
			}}}, nil
		case "tools/call":
			var p callToolParams
			json.Unmarshal(req.Params, &p)
			return callToolResult{Content: []contentItem{{Type: "text", Text: "ran " + p.Name}}}, nil
		default:
			return nil, &RPCError{Code: -32601, Message: "no such method"}
		}
	}
}

func newTestClient(t *testing.T, tr *chanTransport, opts ClientOptions) *Client {
	t.Helper()
	opts.Transport = tr
	if opts.ServerName == "" {
		opts.ServerName = "fake"
	}
	c := NewClient(opts)
	t.Cleanup(func() { c.Close() })
	return c
}

func TestClientHandshakeAndInitializedNotif(t *testing.T) {
	tr := newChanTransport()
	var notifs []string
	var mu sync.Mutex
	tr.serve(t, &notifs, &mu, stdHandler(ProtocolVersion))
	c := newTestClient(t, tr, ClientOptions{})

	if err := c.Initialize(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := c.NegotiatedVersion(); got != ProtocolVersion {
		t.Fatalf("version = %q", got)
	}
	deadline := time.Now().Add(time.Second)
	for {
		mu.Lock()
		sent := len(notifs) > 0 && notifs[0] == "notifications/initialized"
		mu.Unlock()
		if sent {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("initialized notification never sent")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestClientProtocolFallbackAndRejection(t *testing.T) {
	tr := newChanTransport()
	tr.serve(t, nil, nil, stdHandler(FallbackProtocolVersion))
	c := newTestClient(t, tr, ClientOptions{})
	if err := c.Initialize(context.Background()); err != nil {
		t.Fatalf("fallback version must be accepted: %v", err)
	}
	if got := c.NegotiatedVersion(); got != FallbackProtocolVersion {
		t.Fatalf("version = %q", got)
	}

	tr2 := newChanTransport()
	tr2.serve(t, nil, nil, stdHandler("2024-01-01"))
	c2 := newTestClient(t, tr2, ClientOptions{})
	if err := c2.Initialize(context.Background()); err == nil ||
		!strings.Contains(err.Error(), "unsupported protocol version") {
		t.Fatalf("unknown version must be rejected, got %v", err)
	}
}

func TestClientListAndCall(t *testing.T) {
	tr := newChanTransport()
	tr.serve(t, nil, nil, stdHandler(ProtocolVersion))
	c := newTestClient(t, tr, ClientOptions{})

	tools, err := c.ListTools(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("tools = %+v", tools)
	}
	content, isErr, err := c.CallTool(context.Background(), "echo", json.RawMessage(`{"x":1}`))
	if err != nil || isErr {
		t.Fatalf("call: %v isErr=%v", err, isErr)
	}
	if txt := content[0].(ai.TextContent).Text; txt != "ran echo" {
		t.Fatalf("content = %q", txt)
	}
}

func TestClientContentMapping(t *testing.T) {
	tr := newChanTransport()
	tr.serve(t, nil, nil, func(method string, req Request) (any, *RPCError) {
		if method != "tools/call" {
			return stdHandler(ProtocolVersion)(method, req)
		}
		return json.RawMessage(`{"content":[
			{"type":"text","text":"plain"},
			{"type":"image","data":"QUJD","mimeType":"image/png"},
			{"type":"resource","resource":{"uri":"file:///x"}}
		]}`), nil
	})
	c := newTestClient(t, tr, ClientOptions{})

	content, _, err := c.CallTool(context.Background(), "any", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(content) != 3 {
		t.Fatalf("content len = %d", len(content))
	}
	if content[0].(ai.TextContent).Text != "plain" {
		t.Fatal("text block mismapped")
	}
	img := content[1].(ai.ImageContent)
	if img.Data != "QUJD" || img.MimeType != "image/png" {
		t.Fatalf("image block = %+v", img)
	}
	// Unknown type passes through JSON-stringified, nothing dropped.
	if raw := content[2].(ai.TextContent).Text; !strings.Contains(raw, `"resource"`) {
		t.Fatalf("unknown type passthrough = %q", raw)
	}
}

func TestClientCallTimeout(t *testing.T) {
	tr := newChanTransport()
	// No server: requests vanish, so the call must time out.
	c := newTestClient(t, tr, ClientOptions{CallTimeout: 50 * time.Millisecond})

	start := time.Now()
	_, err := c.ListTools(context.Background())
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("err = %v, want timeout", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("timeout took far too long")
	}
}

func TestClientCrashFailsPendingCalls(t *testing.T) {
	tr := newChanTransport()
	c := newTestClient(t, tr, ClientOptions{CallTimeout: 5 * time.Second})

	errCh := make(chan error, 1)
	go func() {
		_, err := c.ListTools(context.Background())
		errCh <- err
	}()
	time.Sleep(20 * time.Millisecond) // let the call register as pending
	tr.Close()                        // EOF = crash

	select {
	case err := <-errCh:
		if err == nil || !strings.Contains(err.Error(), ErrConnClosed.Error()) {
			t.Fatalf("err = %v, want connection closed", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("pending call not failed on crash")
	}
	// New calls on a dead client fail fast, not after a timeout.
	<-c.Done()
	if _, err := c.ListTools(context.Background()); !errors.Is(err, ErrConnClosed) {
		t.Fatalf("post-crash call err = %v", err)
	}
}

func TestClientMalformedLineBurstDropsConnection(t *testing.T) {
	tr := newChanTransport()
	c := newTestClient(t, tr, ClientOptions{})

	// A few bad lines are tolerated…
	for i := 0; i < 3; i++ {
		tr.inject(json.RawMessage("not json at all"))
	}
	select {
	case <-c.Done():
		t.Fatal("connection dropped too eagerly")
	case <-time.After(50 * time.Millisecond):
	}
	// …but a burst past the cap kills the connection.
	for i := 0; i < malformedPerMinuteCap+1; i++ {
		tr.inject(json.RawMessage(fmt.Sprintf("garbage %d", i)))
	}
	select {
	case <-c.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("connection survived malformed-line burst")
	}
}

func TestClientOverlongLineCountsAsMalformed(t *testing.T) {
	// wire.ErrLineTooLong from the transport is recoverable noise, not a
	// dead connection: traffic after it must still route.
	tr := &errOnceTransport{chanTransport: newChanTransport()}
	tr.chanTransport.serve(t, nil, nil, stdHandler(ProtocolVersion))
	c := NewClient(ClientOptions{ServerName: "cap", Transport: tr})
	defer c.Close()

	tools, err := c.ListTools(context.Background())
	if err != nil || len(tools) != 1 {
		t.Fatalf("list after over-long line: %v (%d tools)", err, len(tools))
	}
}

// errOnceTransport injects one ErrLineTooLong before delegating.
type errOnceTransport struct {
	*chanTransport
	once sync.Once
}

func (t *errOnceTransport) Receive(ctx context.Context) (json.RawMessage, error) {
	var fired bool
	t.once.Do(func() { fired = true })
	if fired {
		return nil, wire.ErrLineTooLong
	}
	return t.chanTransport.Receive(ctx)
}

func TestClientToolsListChangedNotification(t *testing.T) {
	tr := newChanTransport()
	fired := make(chan struct{}, 1)
	c := newTestClient(t, tr, ClientOptions{OnToolsChanged: func() {
		select {
		case fired <- struct{}{}:
		default:
		}
	}})
	_ = c

	tr.inject(json.RawMessage(`{"jsonrpc":"2.0","method":"notifications/tools/list_changed"}`))
	select {
	case <-fired:
	case <-time.After(2 * time.Second):
		t.Fatal("OnToolsChanged not fired")
	}

	// Unknown notifications are ignored without dropping the connection.
	tr.inject(json.RawMessage(`{"jsonrpc":"2.0","method":"notifications/whatever"}`))
	select {
	case <-c.Done():
		t.Fatal("unknown notification killed the connection")
	case <-time.After(50 * time.Millisecond):
	}
}
