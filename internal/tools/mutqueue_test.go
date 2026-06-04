package tools

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestMutQueueSerializesSameFile(t *testing.T) {
	q := newMutQueue()
	path := filepath.Join(t.TempDir(), "f.txt")
	var inSection, maxConcurrent int
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			unlock := q.lock(path)
			defer unlock()
			mu.Lock()
			inSection++
			if inSection > maxConcurrent {
				maxConcurrent = inSection
			}
			mu.Unlock()
			time.Sleep(2 * time.Millisecond)
			mu.Lock()
			inSection--
			mu.Unlock()
		}()
	}
	wg.Wait()
	if maxConcurrent != 1 {
		t.Fatalf("same-file critical section saw %d concurrent holders", maxConcurrent)
	}
	if len(q.locks) != 0 {
		t.Fatalf("%d lock entries leaked after release", len(q.locks))
	}
}

func TestMutQueueParallelDifferentFiles(t *testing.T) {
	q := newMutQueue()
	dir := t.TempDir()
	start := make(chan struct{})
	both := make(chan struct{}, 2)
	var wg sync.WaitGroup
	for _, name := range []string{"a.txt", "b.txt"} {
		wg.Add(1)
		go func(p string) {
			defer wg.Done()
			unlock := q.lock(p)
			defer unlock()
			both <- struct{}{}
			<-start // both goroutines must get here while holding their locks
		}(filepath.Join(dir, name))
	}
	for i := 0; i < 2; i++ {
		select {
		case <-both:
		case <-time.After(2 * time.Second):
			t.Fatal("different-file locks blocked each other")
		}
	}
	close(start)
	wg.Wait()
}

func TestMutQueueSymlinkAliasesShareLock(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real.txt")
	if err := os.WriteFile(real, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "alias.txt")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if mutKey(real) != mutKey(link) {
		t.Fatalf("symlink alias got different key: %q vs %q", mutKey(real), mutKey(link))
	}
}

func TestMutKeyMissingFileFallsBackToAbs(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "not-yet.txt")
	key := mutKey(missing)
	if !filepath.IsAbs(key) {
		t.Fatalf("key %q not absolute", key)
	}
}
