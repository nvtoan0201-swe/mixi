package agent

import "testing"

func TestBoundedQueueDrainAll(t *testing.T) {
	q := newBoundedQueue[int](4, DrainAll)
	for i := 1; i <= 3; i++ {
		if err := q.Push(i); err != nil {
			t.Fatal(err)
		}
	}
	got := q.Drain()
	if len(got) != 3 || got[0] != 1 || got[2] != 3 {
		t.Fatalf("DrainAll = %v", got)
	}
	if q.Len() != 0 {
		t.Fatalf("Len after drain = %d", q.Len())
	}
	if q.Drain() != nil {
		t.Fatal("empty drain should be nil")
	}
}

func TestBoundedQueueDrainOne(t *testing.T) {
	q := newBoundedQueue[string](4, DrainOne)
	q.Push("a")
	q.Push("b")
	if got := q.Drain(); len(got) != 1 || got[0] != "a" {
		t.Fatalf("DrainOne #1 = %v", got)
	}
	if got := q.Drain(); len(got) != 1 || got[0] != "b" {
		t.Fatalf("DrainOne #2 = %v", got)
	}
	if q.Len() != 0 {
		t.Fatal("queue should be empty")
	}
}

func TestBoundedQueueRejectsWhenFull(t *testing.T) {
	q := newBoundedQueue[int](2, DrainAll)
	q.Push(1)
	q.Push(2)
	if err := q.Push(3); err != ErrQueueFull {
		t.Fatalf("expected ErrQueueFull, got %v", err)
	}
	// Draining frees capacity again.
	q.Drain()
	if err := q.Push(3); err != nil {
		t.Fatalf("push after drain: %v", err)
	}
}

func TestBoundedQueueDefaultCapacity(t *testing.T) {
	q := newBoundedQueue[int](0, DrainAll)
	for i := 0; i < defaultQueueCap; i++ {
		if err := q.Push(i); err != nil {
			t.Fatalf("push %d: %v", i, err)
		}
	}
	if err := q.Push(99); err != ErrQueueFull {
		t.Fatalf("expected ErrQueueFull at default cap, got %v", err)
	}
}
