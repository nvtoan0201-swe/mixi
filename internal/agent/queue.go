package agent

import (
	"errors"
	"sync"
)

// ErrQueueFull is returned when a steering/follow-up queue is at capacity.
var ErrQueueFull = errors.New("agent: queue full")

// DrainMode controls how many queued items a single Drain returns.
type DrainMode int

const (
	// DrainAll returns every queued item (default).
	DrainAll DrainMode = iota
	// DrainOne returns at most one item per call.
	DrainOne
)

const defaultQueueCap = 64

// boundedQueue is a mutex-guarded FIFO with a hard capacity so a steering
// flood surfaces as ErrQueueFull to the caller instead of unbounded growth.
type boundedQueue[T any] struct {
	mu    sync.Mutex
	items []T
	cap   int
	mode  DrainMode
}

func newBoundedQueue[T any](capacity int, mode DrainMode) *boundedQueue[T] {
	if capacity <= 0 {
		capacity = defaultQueueCap
	}
	return &boundedQueue[T]{cap: capacity, mode: mode}
}

// Push appends item; ErrQueueFull at capacity.
func (q *boundedQueue[T]) Push(item T) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) >= q.cap {
		return ErrQueueFull
	}
	q.items = append(q.items, item)
	return nil
}

// Drain removes and returns items per the queue's drain mode.
func (q *boundedQueue[T]) Drain() []T {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) == 0 {
		return nil
	}
	n := len(q.items)
	if q.mode == DrainOne {
		n = 1
	}
	out := make([]T, n)
	copy(out, q.items[:n])
	q.items = append(q.items[:0], q.items[n:]...)
	return out
}

// Len reports the current queue depth.
func (q *boundedQueue[T]) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.items)
}
