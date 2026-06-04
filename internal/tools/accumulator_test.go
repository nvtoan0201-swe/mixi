package tools

import (
	"bytes"
	"os"
	"strings"
	"sync"
	"testing"
)

func TestAccumulatorSmallStaysInMemory(t *testing.T) {
	a := newAccumulator()
	a.Write([]byte("hello "))
	a.Write([]byte("world"))
	s := a.snapshot()
	if s.Tail != "hello world" || s.Spilled || s.Total != 11 {
		t.Fatalf("snapshot=%+v", s)
	}
}

func TestAccumulatorSpillHoldsFullOutput(t *testing.T) {
	a := newAccumulator()
	chunk := bytes.Repeat([]byte("0123456789abcdef"), 1024) // 16KiB
	const chunks = 640                                      // 10MiB total
	for i := 0; i < chunks; i++ {
		a.Write(chunk)
	}
	s := a.snapshot()
	if !s.Spilled {
		t.Fatal("10MiB output must spill")
	}
	defer os.Remove(s.FilePath)
	if got := len(s.Tail); got > 2*MaxBytes {
		t.Fatalf("memory tail %d exceeds rolling cap %d", got, 2*MaxBytes)
	}
	info, err := os.Stat(s.FilePath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != int64(len(chunk)*chunks) {
		t.Fatalf("spill file %d bytes, want %d", info.Size(), len(chunk)*chunks)
	}
	if s.Total != int64(len(chunk)*chunks) {
		t.Fatalf("total=%d", s.Total)
	}
	a.close()
}

func TestAccumulatorReadFromCursor(t *testing.T) {
	a := newAccumulator()
	a.Write([]byte("first."))
	got, cur, err := a.readFrom(0)
	if err != nil || got != "first." || cur != 6 {
		t.Fatalf("got=%q cur=%d err=%v", got, cur, err)
	}
	a.Write([]byte("second."))
	got, cur, err = a.readFrom(cur)
	if err != nil || got != "second." || cur != 13 {
		t.Fatalf("got=%q cur=%d err=%v", got, cur, err)
	}
	// No new output → empty delta, cursor unchanged.
	got, cur2, _ := a.readFrom(cur)
	if got != "" || cur2 != cur {
		t.Fatalf("got=%q cur=%d", got, cur2)
	}
}

func TestAccumulatorReadFromAfterSpill(t *testing.T) {
	a := newAccumulator()
	big := strings.Repeat("x", 3*MaxBytes)
	a.Write([]byte(big))
	s := a.snapshot()
	if !s.Spilled {
		t.Fatal("expected spill")
	}
	defer os.Remove(s.FilePath)
	got, _, err := a.readFrom(0)
	if err != nil {
		t.Fatal(err)
	}
	if got != big {
		t.Fatalf("spilled readFrom(0) returned %d bytes, want %d", len(got), len(big))
	}
	a.close()
}

func TestAccumulatorPersistFullWithoutSpill(t *testing.T) {
	a := newAccumulator()
	a.Write([]byte("complete output"))
	path := a.persistFull()
	if path == "" {
		t.Fatal("persistFull returned empty path")
	}
	defer os.Remove(path)
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "complete output" {
		t.Fatalf("data=%q err=%v", data, err)
	}
}

func TestAccumulatorConcurrentWriters(t *testing.T) {
	a := newAccumulator()
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 1000; i++ {
				a.Write([]byte("0123456789"))
			}
		}()
	}
	wg.Wait()
	if s := a.snapshot(); s.Total != 40000 {
		t.Fatalf("total=%d, want 40000", s.Total)
	}
}
