package agent

import (
	"context"
	"math/rand"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Retry policy for transient provider failures at the loop level. The
// provider already retries pre-stream HTTP failures; this layer catches
// errors surfaced as StopReasonError stream terminations. Streams that
// already emitted content blocks are never retried here (re-running them
// would duplicate text) — that decision lives in the loop, not the
// classifier.
const (
	retryMaxAttempts = 5
	retryBaseDelay   = 2 * time.Second
	retryJitterFrac  = 0.2
)

// retryClass is the classifier verdict for a failed turn.
type retryClass int

const (
	classFatal retryClass = iota // 4xx auth/validation — surface immediately
	classRetryable
	// classOverflow routes to the compaction path (phase: context
	// management), never to backoff retry.
	classOverflow
)

var (
	httpStatusRe = regexp.MustCompile(`HTTP (\d{3})`)
	// retryAfterRe extracts a server-suggested delay if the provider folded
	// it into the error text (e.g. `"retry_after":30` or "retry after 30s").
	retryAfterRe = regexp.MustCompile(`(?i)retry[-_ ]after["':\s]*(\d+)`)
)

// overflowMarkers match context-window-exceeded errors across providers.
var overflowMarkers = []string{
	"prompt is too long",
	"context length",
	"context window",
	"maximum context",
	"input length and `max_tokens` exceed",
	"too many tokens",
}

// transientMarkers match retryable conditions reported without an HTTP code.
var transientMarkers = []string{
	"overloaded",
	"connection reset",
	"connection refused",
	"unexpected eof",
	"broken pipe",
	"timeout awaiting response",
	"temporary failure",
}

// classifyError buckets a stream error message. Overflow is checked first:
// an HTTP 400 carrying "prompt is too long" must go to compaction, not fatal.
func classifyError(msg string) retryClass {
	lower := strings.ToLower(msg)
	for _, m := range overflowMarkers {
		if strings.Contains(lower, m) {
			return classOverflow
		}
	}
	if m := httpStatusRe.FindStringSubmatch(msg); m != nil {
		code, _ := strconv.Atoi(m[1])
		switch {
		case code == 429 || code >= 500:
			return classRetryable
		default:
			return classFatal
		}
	}
	for _, m := range transientMarkers {
		if strings.Contains(lower, m) {
			return classRetryable
		}
	}
	return classFatal
}

// retryDelay computes the wait before attempt n (1-based): the server's
// suggested delay when present in the message, else base·2^(n-1) ±20%
// jitter. base ≤ 0 selects the production default.
func retryDelay(attempt int, errMsg string, rnd *rand.Rand, base time.Duration) time.Duration {
	if m := retryAfterRe.FindStringSubmatch(errMsg); m != nil {
		if secs, err := strconv.Atoi(m[1]); err == nil && secs >= 0 {
			return time.Duration(secs) * time.Second
		}
	}
	if base <= 0 {
		base = retryBaseDelay
	}
	base <<= attempt - 1
	jitter := 1 + retryJitterFrac*(2*rnd.Float64()-1)
	return time.Duration(float64(base) * jitter)
}

// sleepCtx waits d or until ctx is cancelled.
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
