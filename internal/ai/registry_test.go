package ai

import (
	"context"
	"fmt"
	"sync"
	"testing"
)

// fakeProvider emits a scripted event sequence and closes the channel.
type fakeProvider struct {
	api    string
	events func(model Model) []StreamEvent
}

func (f fakeProvider) API() string { return f.api }

func (f fakeProvider) Stream(ctx context.Context, model Model, c Context, opts StreamOptions) <-chan StreamEvent {
	ch := make(chan StreamEvent)
	go func() {
		defer close(ch)
		for _, ev := range f.events(model) {
			select {
			case ch <- ev:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch
}

func TestRegisterAndResolve(t *testing.T) {
	p := fakeProvider{api: "test-resolve"}
	Register(p)
	got, err := Resolve("test-resolve")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.API() != "test-resolve" {
		t.Errorf("resolved API = %q, want %q", got.API(), "test-resolve")
	}
}

func TestResolveUnknownAPI(t *testing.T) {
	if _, err := Resolve("no-such-api"); err == nil {
		t.Error("Resolve(unknown) = nil error, want error")
	}
}

func TestRegisterDuplicatePanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("duplicate Register did not panic")
		}
	}()
	Register(fakeProvider{api: "test-dup"})
	Register(fakeProvider{api: "test-dup"})
}

func TestRegistryConcurrentAccess(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(2)
		go func(n int) {
			defer wg.Done()
			Register(fakeProvider{api: fmt.Sprintf("test-race-%d", n)})
		}(i)
		go func(n int) {
			defer wg.Done()
			_, _ = Resolve(fmt.Sprintf("test-race-%d", n))
		}(i)
	}
	wg.Wait()
}
