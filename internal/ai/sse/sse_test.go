package sse

import (
	"io"
	"os"
	"strings"
	"testing"
)

// chunkReader yields at most n bytes per Read to simulate events split
// across network reads.
type chunkReader struct {
	data []byte
	n    int
}

func (c *chunkReader) Read(p []byte) (int, error) {
	if len(c.data) == 0 {
		return 0, io.EOF
	}
	n := c.n
	if n > len(c.data) {
		n = len(c.data)
	}
	if n > len(p) {
		n = len(p)
	}
	copy(p, c.data[:n])
	c.data = c.data[n:]
	return n, nil
}

func drain(t *testing.T, d *Decoder) []Event {
	t.Helper()
	var events []Event
	for {
		ev, err := d.Next()
		if err == io.EOF {
			return events
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		events = append(events, ev)
	}
}

// goldenEvents is the expected decode of testdata/anthropic_stream.txt,
// which mixes \r\n, \n, and lone-\r line endings and includes a ping event
// and a comment line (both skipped).
var goldenEvents = []Event{
	{"message_start", `{"type":"message_start","message":{"id":"msg_01","usage":{"input_tokens":10,"output_tokens":1}}}`},
	{"content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`},
	{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}`},
	{"content_block_stop", `{"type":"content_block_stop","index":0}`},
	{"message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}`},
	{"message_stop", `{"type":"message_stop"}`},
}

func TestDecodeAnthropicFixture(t *testing.T) {
	raw, err := os.ReadFile("testdata/anthropic_stream.txt")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	for _, chunk := range []int{1, 3, 7, len(raw)} {
		got := drain(t, NewDecoder(&chunkReader{data: raw, n: chunk}))
		if len(got) != len(goldenEvents) {
			t.Fatalf("chunk=%d: %d events, want %d: %+v", chunk, len(got), len(goldenEvents), got)
		}
		for i, want := range goldenEvents {
			if got[i] != want {
				t.Errorf("chunk=%d event[%d]:\n got %+v\nwant %+v", chunk, i, got[i], want)
			}
		}
	}
}

func TestMultiLineDataJoined(t *testing.T) {
	in := "event: foo\ndata: line1\ndata: line2\n\n"
	got := drain(t, NewDecoder(strings.NewReader(in)))
	want := []Event{{"foo", "line1\nline2"}}
	if len(got) != 1 || got[0] != want[0] {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestDataWithoutEventName(t *testing.T) {
	in := "data: bare\n\n"
	got := drain(t, NewDecoder(strings.NewReader(in)))
	if len(got) != 1 || got[0] != (Event{"", "bare"}) {
		t.Errorf("got %+v", got)
	}
}

func TestNoSpaceAfterColon(t *testing.T) {
	in := "event:foo\ndata:bar\n\n"
	got := drain(t, NewDecoder(strings.NewReader(in)))
	if len(got) != 1 || got[0] != (Event{"foo", "bar"}) {
		t.Errorf("got %+v", got)
	}
}

func TestEventWithoutDataNotDispatched(t *testing.T) {
	in := "event: lonely\n\nevent: real\ndata: x\n\n"
	got := drain(t, NewDecoder(strings.NewReader(in)))
	if len(got) != 1 || got[0] != (Event{"real", "x"}) {
		t.Errorf("got %+v", got)
	}
}

func TestPendingEventDispatchedAtEOF(t *testing.T) {
	in := "event: cut\ndata: short" // no trailing blank line
	got := drain(t, NewDecoder(strings.NewReader(in)))
	if len(got) != 1 || got[0] != (Event{"cut", "short"}) {
		t.Errorf("got %+v", got)
	}
}

func TestOversizedLineErrors(t *testing.T) {
	in := "data: " + strings.Repeat("x", maxLineBytes+1) + "\n\n"
	d := NewDecoder(strings.NewReader(in))
	if _, err := d.Next(); err == nil || err == io.EOF {
		t.Errorf("oversized line: err = %v, want non-EOF error", err)
	}
}
