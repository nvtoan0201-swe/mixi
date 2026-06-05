package wire

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
)

func TestReaderBasicAndBlankLines(t *testing.T) {
	src := "{\"a\":1}\n\n{\"b\":2}\n"
	r := NewReader(strings.NewReader(src))

	for _, want := range []string{`{"a":1}`, `{"b":2}`} {
		got, err := r.ReadLine()
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Fatalf("ReadLine = %q, want %q", got, want)
		}
	}
	if _, err := r.ReadLine(); err != io.EOF {
		t.Fatalf("after last line err = %v, want EOF", err)
	}
}

func TestReaderUnterminatedFinalLine(t *testing.T) {
	r := NewReader(strings.NewReader(`{"tail":true}`))
	got, err := r.ReadLine()
	if err != nil || string(got) != `{"tail":true}` {
		t.Fatalf("ReadLine = %q, %v", got, err)
	}
	if _, err := r.ReadLine(); err != io.EOF {
		t.Fatalf("err = %v, want EOF", err)
	}
}

func TestReaderLineCapSkipsToNextMessage(t *testing.T) {
	big := strings.Repeat("x", MaxLineBytes+10)
	src := big + "\n{\"ok\":1}\n"
	r := NewReader(strings.NewReader(src))

	if _, err := r.ReadLine(); !errors.Is(err, ErrLineTooLong) {
		t.Fatalf("err = %v, want ErrLineTooLong", err)
	}
	// The over-long line must be fully discarded: the next read returns the
	// following message, not mid-line garbage.
	got, err := r.ReadLine()
	if err != nil || string(got) != `{"ok":1}` {
		t.Fatalf("after cap: %q, %v", got, err)
	}
}

func TestReaderLongLineUnderCap(t *testing.T) {
	// Longer than the bufio buffer (64KiB) but under the cap: must be
	// reassembled from isPrefix chunks.
	payload := strings.Repeat("y", 200<<10)
	r := NewReader(strings.NewReader(payload + "\n"))
	got, err := r.ReadLine()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(payload) {
		t.Fatalf("len = %d, want %d", len(got), len(payload))
	}
}

func TestWriterRejectsOversize(t *testing.T) {
	w := NewWriter(io.Discard)
	if err := w.WriteLine(make([]byte, MaxLineBytes+1)); !errors.Is(err, ErrLineTooLong) {
		t.Fatalf("err = %v, want ErrLineTooLong", err)
	}
}

// syncBuffer serializes Write calls and records each one whole, so a torn
// (interleaved) write from Writer would surface as an unparsable line.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func TestWriterConcurrentWritesAreAtomic(t *testing.T) {
	var buf syncBuffer
	w := NewWriter(&buf)

	const writers, perWriter = 8, 50
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			msg, _ := json.Marshal(map[string]any{"writer": id, "pad": strings.Repeat("p", 256)})
			for j := 0; j < perWriter; j++ {
				if err := w.WriteLine(msg); err != nil {
					t.Error(err)
					return
				}
			}
		}(i)
	}
	wg.Wait()

	lines := strings.Split(strings.TrimRight(buf.buf.String(), "\n"), "\n")
	if len(lines) != writers*perWriter {
		t.Fatalf("lines = %d, want %d", len(lines), writers*perWriter)
	}
	for _, ln := range lines {
		if !json.Valid([]byte(ln)) {
			t.Fatalf("torn write, invalid JSON line: %.80q", ln)
		}
	}
}

func TestRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf)
	msgs := []string{`{"id":1}`, `{"id":2,"method":"x"}`}
	for _, m := range msgs {
		if err := w.WriteLine([]byte(m)); err != nil {
			t.Fatal(err)
		}
	}
	r := NewReader(&buf)
	for _, want := range msgs {
		got, err := r.ReadLine()
		if err != nil || string(got) != want {
			t.Fatalf("round trip = %q, %v; want %q", got, err, want)
		}
	}
}
