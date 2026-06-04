package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// recordingObserver captures observe calls for assertions.
type recordingObserver struct {
	mu     sync.Mutex
	reads  map[string]int
	writes map[string]int
}

func newRecordingObserver() *recordingObserver {
	return &recordingObserver{reads: map[string]int{}, writes: map[string]int{}}
}

func (o *recordingObserver) ObserveRead(path string, data []byte) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.reads[path] += len(data)
}

func (o *recordingObserver) ObserveWrite(path string, data []byte) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.writes[path] += len(data)
}

func TestFileToolsNotifyObserver(t *testing.T) {
	dir := t.TempDir()
	obs := newRecordingObserver()
	src := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(src, []byte("hello world"), 0o644); err != nil {
		t.Fatal(err)
	}

	read := &readTool{cwd: dir, obs: obs}
	if _, err := read.Execute(context.Background(), json.RawMessage(`{"path":"a.txt"}`), nil); err != nil {
		t.Fatal(err)
	}
	if obs.reads[src] != len("hello world") {
		t.Fatalf("read not observed: %v", obs.reads)
	}

	mq := newMutQueue()
	write := &writeTool{cwd: dir, mq: mq, obs: obs}
	dst := filepath.Join(dir, "b.txt")
	if _, err := write.Execute(context.Background(), json.RawMessage(`{"path":"b.txt","content":"fresh"}`), nil); err != nil {
		t.Fatal(err)
	}
	if obs.writes[dst] != len("fresh") {
		t.Fatalf("write not observed: %v", obs.writes)
	}

	edit := &editTool{cwd: dir, mq: mq, obs: obs}
	args := `{"path":"b.txt","edits":[{"oldText":"fresh","newText":"fresher"}]}`
	if _, err := edit.Execute(context.Background(), json.RawMessage(args), nil); err != nil {
		t.Fatal(err)
	}
	if obs.writes[dst] != len("fresh")+len("fresher") {
		t.Fatalf("edit not observed: %v", obs.writes)
	}
}

func TestObserverSkippedOnErrorResults(t *testing.T) {
	dir := t.TempDir()
	obs := newRecordingObserver()

	read := &readTool{cwd: dir, obs: obs}
	if res, err := read.Execute(context.Background(), json.RawMessage(`{"path":"missing.txt"}`), nil); err != nil || !res.IsError {
		t.Fatalf("expected error result, got %+v err=%v", res, err)
	}

	edit := &editTool{cwd: dir, mq: newMutQueue(), obs: obs}
	target := filepath.Join(dir, "c.txt")
	if err := os.WriteFile(target, []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}
	args := `{"path":"c.txt","edits":[{"oldText":"absent","newText":"x"}]}`
	if res, err := edit.Execute(context.Background(), json.RawMessage(args), nil); err != nil || !res.IsError {
		t.Fatalf("expected error result, got %+v err=%v", res, err)
	}

	if len(obs.reads)+len(obs.writes) != 0 {
		t.Fatalf("error results observed: reads=%v writes=%v", obs.reads, obs.writes)
	}
}

func TestNilObserverIsSafe(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	read := &readTool{cwd: dir}
	if _, err := read.Execute(context.Background(), json.RawMessage(`{"path":"a.txt"}`), nil); err != nil {
		t.Fatal(err)
	}
	write := &writeTool{cwd: dir, mq: newMutQueue()}
	if _, err := write.Execute(context.Background(), json.RawMessage(`{"path":"b.txt","content":"y"}`), nil); err != nil {
		t.Fatal(err)
	}
}

func TestObserverConcurrentToolUse(t *testing.T) {
	dir := t.TempDir()
	obs := newRecordingObserver()
	mq := newMutQueue()
	write := &writeTool{cwd: dir, mq: mq, obs: obs}

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			args := fmt.Sprintf(`{"path":"f%d.txt","content":"data"}`, i)
			if _, err := write.Execute(context.Background(), json.RawMessage(args), nil); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	if len(obs.writes) != 8 {
		t.Fatalf("writes observed = %d, want 8", len(obs.writes))
	}
}
