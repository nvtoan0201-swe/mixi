package agent

import (
	"math/rand"
	"testing"
	"time"
)

func TestClassifyError(t *testing.T) {
	cases := []struct {
		msg  string
		want retryClass
	}{
		{"anthropic: HTTP 429: rate limited", classRetryable},
		{"anthropic: HTTP 500: internal", classRetryable},
		{"HTTP 502: bad gateway", classRetryable},
		{"HTTP 503: unavailable", classRetryable},
		{"HTTP 504: gateway timeout", classRetryable},
		{"HTTP 529: overloaded", classRetryable},
		{"anthropic: HTTP 400: invalid request", classFatal},
		{"anthropic: HTTP 401: bad key", classFatal},
		{"anthropic: HTTP 403: forbidden", classFatal},
		{`{"type":"overloaded_error"}`, classRetryable},
		{"read tcp: connection reset by peer", classRetryable},
		{"unexpected EOF", classRetryable},
		{"dial tcp: connection refused", classRetryable},
		{"write: broken pipe", classRetryable},
		{"HTTP 400: prompt is too long: 250000 tokens", classOverflow},
		{"input exceeds the context window of this model", classOverflow},
		{"maximum context length is 200000 tokens", classOverflow},
		{"something inexplicable", classFatal},
		{"", classFatal},
	}
	for _, c := range cases {
		if got := classifyError(c.msg); got != c.want {
			t.Errorf("classifyError(%q) = %v, want %v", c.msg, got, c.want)
		}
	}
}

func TestRetryDelayJitterBounds(t *testing.T) {
	rnd := rand.New(rand.NewSource(42))
	for attempt := 1; attempt <= 5; attempt++ {
		base := retryBaseDelay << (attempt - 1)
		lo := time.Duration(float64(base) * (1 - retryJitterFrac))
		hi := time.Duration(float64(base) * (1 + retryJitterFrac))
		for i := 0; i < 50; i++ {
			d := retryDelay(attempt, "HTTP 500", rnd, 0)
			if d < lo || d > hi {
				t.Fatalf("attempt %d: delay %v outside [%v, %v]", attempt, d, lo, hi)
			}
		}
	}
}

func TestRetryDelayHonorsRetryAfter(t *testing.T) {
	rnd := rand.New(rand.NewSource(1))
	cases := []struct {
		msg  string
		want time.Duration
	}{
		{`HTTP 429: {"retry_after": 7}`, 7 * time.Second},
		{"HTTP 429: Retry-After: 30", 30 * time.Second},
		{"HTTP 429: retry after 0 seconds", 0},
	}
	for _, c := range cases {
		if got := retryDelay(1, c.msg, rnd, 0); got != c.want {
			t.Errorf("retryDelay(%q) = %v, want %v", c.msg, got, c.want)
		}
	}
}

func TestRetryDelayCustomBase(t *testing.T) {
	rnd := rand.New(rand.NewSource(1))
	d := retryDelay(1, "HTTP 500", rnd, time.Millisecond)
	if d > 2*time.Millisecond {
		t.Fatalf("custom base ignored: %v", d)
	}
}
