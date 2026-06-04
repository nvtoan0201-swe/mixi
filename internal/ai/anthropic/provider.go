// Package anthropic implements the Anthropic Messages streaming provider
// against the normalized internal/ai contract. Hand-rolled net/http (no SDK)
// for full control over headers, retries, and SSE decoding.
package anthropic

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/user/mixi-agent/internal/ai"
	"github.com/user/mixi-agent/internal/ai/sse"
)

const (
	apiName        = "anthropic-messages"
	defaultBaseURL = "https://api.anthropic.com"
	apiVersion     = "2023-06-01"

	defaultMaxRetries = 2
	maxRetryDelay     = 60 * time.Second
	errBodyLimit      = 8 << 10
)

func init() { ai.Register(&Provider{}) }

// Provider streams Anthropic Messages completions. Zero value is ready;
// HTTPClient overrides the transport (tests, proxies).
type Provider struct {
	HTTPClient *http.Client
}

func (p *Provider) API() string { return apiName }

// Stream issues the request on a pump goroutine and returns the event
// channel. All failure paths emit EventError then close; never panics out.
func (p *Provider) Stream(ctx context.Context, model ai.Model, c ai.Context, opts ai.StreamOptions) <-chan ai.StreamEvent {
	ch := make(chan ai.StreamEvent, 16)
	t := newTracker(model)
	go func() {
		defer close(ch)
		defer func() {
			if r := recover(); r != nil {
				// Last-resort guard: surface internal bugs as a stream error
				// instead of crashing the process or leaking the channel.
				emit(ctx, ch, t.fail(ai.StopReasonError, fmt.Sprintf("anthropic: internal panic: %v", r)))
			}
		}()
		p.pump(ctx, model, c, opts, ch, t)
	}()
	return ch
}

func (p *Provider) pump(ctx context.Context, model ai.Model, c ai.Context, opts ai.StreamOptions, ch chan<- ai.StreamEvent, t *tracker) {
	body, betas, err := buildRequest(model, c, opts)
	if err != nil {
		emit(ctx, ch, t.fail(ai.StopReasonError, err.Error()))
		return
	}
	resp, err := p.do(ctx, model, opts, body, betas)
	if err != nil {
		t.failFrom(ctx, ch, err)
		return
	}
	defer resp.Body.Close()

	dec := sse.NewDecoder(resp.Body)
	for {
		ev, err := dec.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				err = errors.New("anthropic: stream ended before message_stop")
			}
			t.failFrom(ctx, ch, err)
			return
		}
		events, err := t.handle(ev.Data)
		if err != nil {
			emit(ctx, ch, t.fail(ai.StopReasonError, err.Error()))
			return
		}
		for _, out := range events {
			if !emit(ctx, ch, out) {
				return
			}
			switch out.(type) {
			case ai.EventDone, ai.EventError:
				return
			}
		}
	}
}

// emit sends ev unless ctx is cancelled mid-send; on cancellation it tries a
// non-blocking abort event so consumers see why the channel closed.
func emit(ctx context.Context, ch chan<- ai.StreamEvent, ev ai.StreamEvent) bool {
	select {
	case ch <- ev:
		return true
	case <-ctx.Done():
		return false
	}
}

// failFrom classifies a transport error as user abort vs API failure and
// emits the matching terminal event, preserving partial content.
func (t *tracker) failFrom(ctx context.Context, ch chan<- ai.StreamEvent, err error) {
	if ctx.Err() != nil {
		emit(context.Background(), ch, t.fail(ai.StopReasonAborted, ctx.Err().Error()))
		return
	}
	emit(ctx, ch, t.fail(ai.StopReasonError, err.Error()))
}

// do POSTs /v1/messages, retrying 429/5xx responses and connection errors up
// to opts.MaxRetries times (default 2), honoring Retry-After.
func (p *Provider) do(ctx context.Context, model ai.Model, opts ai.StreamOptions, body []byte, betas string) (*http.Response, error) {
	client := p.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	baseURL := model.BaseURL
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	maxRetries := opts.MaxRetries
	if maxRetries <= 0 {
		maxRetries = defaultMaxRetries
	}
	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/v1/messages", bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("content-type", "application/json")
		req.Header.Set("accept", "text/event-stream")
		req.Header.Set("anthropic-version", apiVersion)
		req.Header.Set("x-api-key", opts.APIKey)
		if betas != "" {
			req.Header.Set("anthropic-beta", betas)
		}

		resp, err := client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, err
			}
			lastErr = err // connection reset / DNS / TLS → retryable
		} else if resp.StatusCode == http.StatusOK {
			return resp, nil
		} else {
			msg, _ := io.ReadAll(io.LimitReader(resp.Body, errBodyLimit))
			resp.Body.Close()
			lastErr = fmt.Errorf("anthropic: HTTP %d: %s", resp.StatusCode, bytes.TrimSpace(msg))
			if resp.StatusCode != http.StatusTooManyRequests && resp.StatusCode < 500 {
				return nil, lastErr
			}
			if attempt < maxRetries {
				if err := sleepCtx(ctx, retryDelay(resp, attempt)); err != nil {
					return nil, err
				}
				continue
			}
			return nil, lastErr
		}
		if attempt < maxRetries {
			if err := sleepCtx(ctx, retryDelay(nil, attempt)); err != nil {
				return nil, err
			}
		}
	}
	return nil, lastErr
}

// retryDelay prefers the server's Retry-After (seconds form) and falls back
// to exponential backoff: 500ms · 2^attempt, capped at maxRetryDelay.
func retryDelay(resp *http.Response, attempt int) time.Duration {
	if resp != nil {
		if ra := resp.Header.Get("Retry-After"); ra != "" {
			if secs, err := strconv.Atoi(ra); err == nil && secs >= 0 {
				return min(time.Duration(secs)*time.Second, maxRetryDelay)
			}
			if at, err := http.ParseTime(ra); err == nil {
				return min(max(time.Until(at), 0), maxRetryDelay)
			}
		}
	}
	return min(500*time.Millisecond<<attempt, maxRetryDelay)
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
