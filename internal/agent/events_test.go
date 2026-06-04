package agent

import (
	"sync"
	"testing"
	"time"

	"github.com/user/mixi-agent/internal/ai"
)

func TestEventBusDeliversToAllSubscribers(t *testing.T) {
	b := newEventBus(silentLog)
	ch1, cancel1 := b.Subscribe()
	ch2, cancel2 := b.Subscribe()
	defer cancel1()
	defer cancel2()

	b.Publish(EvAgentStart{})
	for i, ch := range []<-chan Event{ch1, ch2} {
		select {
		case ev := <-ch:
			if _, ok := ev.(EvAgentStart); !ok {
				t.Fatalf("subscriber %d got %T", i, ev)
			}
		case <-time.After(time.Second):
			t.Fatalf("subscriber %d timed out", i)
		}
	}
}

func TestEventBusUnsubscribeClosesChannel(t *testing.T) {
	b := newEventBus(silentLog)
	ch, cancel := b.Subscribe()
	cancel()
	cancel() // idempotent
	if _, open := <-ch; open {
		t.Fatal("channel should be closed after unsubscribe")
	}
	b.Publish(EvAgentStart{}) // must not panic on closed subscriber
}

func TestEventBusDropsRenderEventsWhenFull(t *testing.T) {
	b := newEventBus(silentLog)
	ch, cancel := b.Subscribe()
	defer cancel()

	// Fill the buffer without consuming, then overflow with render events.
	for i := 0; i < subscriberBuf; i++ {
		b.Publish(EvMessageUpdate{StreamEvent: ai.EventTextDelta{Delta: "x"}})
	}
	b.Publish(EvMessageUpdate{StreamEvent: ai.EventTextDelta{Delta: "dropped"}})
	b.Publish(EvToolUpdate{CallID: "c", Partial: "dropped"})

	// Channel still holds exactly the buffered events; subscriber survives.
	if got := len(ch); got != subscriberBuf {
		t.Fatalf("buffered = %d, want %d", got, subscriberBuf)
	}
	<-ch // free one slot
	b.Publish(EvNotice{Text: "lifecycle"})
	// Drain: the lifecycle event must be present at the tail.
	var last Event
	for len(ch) > 0 {
		last = <-ch
	}
	if _, ok := last.(EvNotice); !ok {
		t.Fatalf("lifecycle event lost, tail = %T", last)
	}
}

func TestEventBusDisconnectsSlowSubscriberOnLifecycle(t *testing.T) {
	b := newEventBus(silentLog)
	ch, cancel := b.Subscribe()
	defer cancel()

	for i := 0; i < subscriberBuf; i++ {
		b.Publish(EvNotice{Text: "fill"})
	}
	start := time.Now()
	b.Publish(EvAgentEnd{Reason: EndDone}) // blocks ≤1s, then disconnects
	if elapsed := time.Since(start); elapsed < lifecycleBlockMax {
		t.Fatalf("disconnected too early: %v", elapsed)
	}
	// Drain everything; the channel must be closed (subscriber cut).
	for {
		if _, open := <-ch; !open {
			return
		}
	}
}

func TestEventBusPublishDoesNotBlockOtherSubscribers(t *testing.T) {
	b := newEventBus(silentLog)
	slow, cancelSlow := b.Subscribe()
	defer cancelSlow()
	fast, cancelFast := b.Subscribe()
	defer cancelFast()

	// Saturate the slow subscriber so the next lifecycle publish enters its
	// grace period; the fast subscriber must still receive promptly.
	for i := 0; i < subscriberBuf; i++ {
		b.Publish(EvNotice{Text: "fill"})
	}
	for len(fast) > 0 {
		<-fast
	}

	done := make(chan struct{})
	go func() {
		b.Publish(EvAgentEnd{Reason: EndDone})
		close(done)
	}()
	// While the publish is parked on the slow subscriber, a new Subscribe
	// must not block (registration lock is free).
	subscribed := make(chan struct{})
	go func() {
		_, cancel := b.Subscribe()
		cancel()
		close(subscribed)
	}()
	select {
	case <-subscribed:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Subscribe blocked while a lifecycle publish was in its grace period")
	}
	<-done
	_ = slow
}

func TestEventBusConcurrentPublishSubscribe(t *testing.T) {
	b := newEventBus(silentLog)
	var wg sync.WaitGroup
	for p := 0; p < 4; p++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				b.Publish(EvMessageUpdate{StreamEvent: ai.EventTextDelta{Delta: "x"}})
			}
		}()
	}
	for s := 0; s < 4; s++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ch, cancel := b.Subscribe()
			for i := 0; i < 50; i++ {
				select {
				case <-ch:
				case <-time.After(10 * time.Millisecond):
				}
			}
			cancel()
		}()
	}
	wg.Wait()
	b.CloseAll()
}
