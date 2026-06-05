package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/user/mixi-agent/internal/wire"
)

// DefaultCallTimeout bounds a single RPC when the server config sets none.
const DefaultCallTimeout = 30 * time.Second

// malformedPerMinuteCap is how many unparsable stdout lines a server may
// emit before the client gives up on the connection (→ restart).
const malformedPerMinuteCap = 10

// ErrConnClosed reports the transport died (crash or close) while a call
// was pending or being issued.
var ErrConnClosed = errors.New("mcp: connection closed")

// ClientOptions configures one server connection.
type ClientOptions struct {
	ServerName  string
	Transport   Transport
	CallTimeout time.Duration // 0 = DefaultCallTimeout
	Log         *slog.Logger  // nil = default
	// OnToolsChanged fires (from the reader goroutine) when the server sends
	// notifications/tools/list_changed. May be nil.
	OnToolsChanged func()
}

// Client speaks JSON-RPC to one MCP server: handshake, tools/list,
// tools/call, with id-tracked routing and per-call timeouts.
type Client struct {
	name    string
	tr      Transport
	timeout time.Duration
	log     *slog.Logger
	onTools func()

	nextID  atomic.Int64
	mu      sync.Mutex
	pending map[int64]chan callOutcome
	closed  bool
	done    chan struct{} // closed when the reader goroutine exits

	protocolVersion string // negotiated during Initialize
}

// NewClient wraps an open transport and starts the reader goroutine. The
// caller must run Initialize before issuing other calls.
func NewClient(opts ClientOptions) *Client {
	c := &Client{
		name:    opts.ServerName,
		tr:      opts.Transport,
		timeout: opts.CallTimeout,
		log:     opts.Log,
		onTools: opts.OnToolsChanged,
		pending: map[int64]chan callOutcome{},
		done:    make(chan struct{}),
	}
	if c.timeout <= 0 {
		c.timeout = DefaultCallTimeout
	}
	if c.log == nil {
		c.log = slog.Default()
	}
	go c.readLoop()
	return c
}

// Done is closed once the connection is dead (crash, EOF, or Close); the
// manager watches it to drive restarts.
func (c *Client) Done() <-chan struct{} { return c.done }

// Close fails all pending calls and tears down the transport.
func (c *Client) Close() error {
	c.failAll(ErrConnClosed)
	return c.tr.Close()
}

// readLoop routes responses by id and dispatches notifications. It exits on
// transport EOF/crash, failing every pending call.
func (c *Client) readLoop() {
	defer close(c.done)
	defer c.failAll(ErrConnClosed)
	var malformed int
	windowStart := time.Now()
	for {
		raw, err := c.tr.Receive(context.Background())
		if err != nil && !errors.Is(err, wire.ErrLineTooLong) {
			return // EOF or crash — transport is dead
		}
		var env struct {
			ID     *int64          `json:"id"`
			Method string          `json:"method"`
			Result json.RawMessage `json:"result"`
			Error  *RPCError       `json:"error"`
		}
		if raw == nil || json.Unmarshal(raw, &env) != nil {
			// Non-JSON noise on stdout (or an over-long line). Tolerate a
			// burst, then declare the connection unusable.
			if time.Since(windowStart) > time.Minute {
				malformed, windowStart = 0, time.Now()
			}
			malformed++
			c.log.Warn("mcp: malformed stdout line", "server", c.name, "count", malformed)
			if malformed > malformedPerMinuteCap {
				c.log.Warn("mcp: too many malformed lines, dropping connection", "server", c.name)
				c.tr.Close()
				return
			}
			continue
		}
		switch {
		case env.ID != nil && env.Method == "": // response
			c.route(*env.ID, raw)
		case env.Method != "": // notification (or server→client request, unsupported)
			c.notify(env.Method)
		}
	}
}

// callOutcome resolves one pending call: a routed response or a transport-
// level error (crash, close).
type callOutcome struct {
	resp *Response
	err  error
}

func (c *Client) route(id int64, raw json.RawMessage) {
	var resp Response
	if err := json.Unmarshal(raw, &resp); err != nil {
		c.log.Warn("mcp: bad response", "server", c.name, "err", err)
		return
	}
	c.mu.Lock()
	ch, ok := c.pending[id]
	delete(c.pending, id)
	c.mu.Unlock()
	if !ok {
		c.log.Warn("mcp: response for unknown id", "server", c.name, "id", id)
		return
	}
	ch <- callOutcome{resp: &resp}
}

func (c *Client) notify(method string) {
	switch method {
	case "notifications/tools/list_changed":
		if c.onTools != nil {
			c.onTools()
		}
	default:
		c.log.Debug("mcp: ignoring notification", "server", c.name, "method", method)
	}
}

// failAll resolves every pending call with err.
func (c *Client) failAll(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	for id, ch := range c.pending {
		delete(c.pending, id)
		ch <- callOutcome{err: err}
	}
}

// call issues one request and waits for its response, the per-call timeout,
// or ctx cancellation.
func (c *Client) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := c.nextID.Add(1)
	var rawParams json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return nil, fmt.Errorf("mcp %s: marshal params: %w", c.name, err)
		}
		rawParams = b
	}
	body, err := json.Marshal(Request{JSONRPC: "2.0", ID: id, Method: method, Params: rawParams})
	if err != nil {
		return nil, err
	}

	// Buffered so a late route/failAll never blocks on an abandoned call.
	ch := make(chan callOutcome, 1)
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, fmt.Errorf("mcp %s: %w", c.name, ErrConnClosed)
	}
	c.pending[id] = ch
	c.mu.Unlock()

	if err := c.tr.Send(ctx, body); err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, fmt.Errorf("mcp %s: send %s: %w", c.name, method, err)
	}

	timer := time.NewTimer(c.timeout)
	defer timer.Stop()
	select {
	case out := <-ch:
		if out.err != nil {
			return nil, fmt.Errorf("mcp %s: %s: %w", c.name, method, out.err)
		}
		if out.resp.Error != nil {
			return nil, fmt.Errorf("mcp %s: %s: %w", c.name, method, out.resp.Error)
		}
		return out.resp.Result, nil
	case <-timer.C:
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, fmt.Errorf("mcp %s: %s timed out after %s", c.name, method, c.timeout)
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, ctx.Err()
	}
}
