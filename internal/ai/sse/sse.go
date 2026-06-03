// Package sse decodes Server-Sent Event streams as emitted by LLM APIs.
package sse

import (
	"bufio"
	"bytes"
	"io"
	"strings"
)

// maxLineBytes caps a single SSE line; oversized lines abort the stream
// instead of growing memory unboundedly.
const maxLineBytes = 1 << 20 // 1 MiB

// Event is one dispatched SSE event: the optional "event:" name and the
// accumulated "data:" payload (multiple data lines joined with "\n").
type Event struct {
	Event string
	Data  string
}

// Decoder reads SSE events from an io.Reader. It handles \r\n, \n, and \r
// line endings, ignores ":" comment lines and "ping" events, and dispatches
// on blank lines per the SSE spec.
type Decoder struct {
	s *bufio.Scanner
}

// NewDecoder wraps r with a line-capped SSE decoder.
func NewDecoder(r io.Reader) *Decoder {
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 0, 64*1024), maxLineBytes)
	s.Split(scanEOL)
	return &Decoder{s: s}
}

// Next returns the next event, or io.EOF when the stream ends. A pending
// event at EOF (data without a trailing blank line) is dispatched before
// io.EOF is reported.
func (d *Decoder) Next() (Event, error) {
	var ev Event
	var data []string
	for {
		if !d.s.Scan() {
			if err := d.s.Err(); err != nil {
				return Event{}, err
			}
			if len(data) > 0 && ev.Event != "ping" {
				ev.Data = strings.Join(data, "\n")
				return ev, nil
			}
			return Event{}, io.EOF
		}
		line := d.s.Text()
		switch {
		case line == "": // blank line → dispatch accumulated event
			if len(data) == 0 || ev.Event == "ping" {
				// Empty data buffer (spec: no dispatch) or keepalive — reset.
				ev, data = Event{}, nil
				continue
			}
			ev.Data = strings.Join(data, "\n")
			return ev, nil
		case strings.HasPrefix(line, ":"): // comment (e.g. ": keepalive")
			continue
		case strings.HasPrefix(line, "event:"):
			ev.Event = trimFieldValue(line[len("event:"):])
		case strings.HasPrefix(line, "data:"):
			data = append(data, trimFieldValue(line[len("data:"):]))
		default:
			// Unknown field (id:, retry:, bare names) — ignored.
		}
	}
}

// trimFieldValue strips the single optional space after the field colon.
func trimFieldValue(v string) string {
	return strings.TrimPrefix(v, " ")
}

// scanEOL is a bufio.SplitFunc that treats \r\n, \n, and lone \r as line
// terminators.
func scanEOL(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}
	if i := bytes.IndexAny(data, "\r\n"); i >= 0 {
		if data[i] == '\n' {
			return i + 1, data[:i], nil
		}
		// \r: need one more byte to know whether it is part of \r\n.
		if i+1 < len(data) {
			if data[i+1] == '\n' {
				return i + 2, data[:i], nil
			}
			return i + 1, data[:i], nil
		}
		if atEOF {
			return i + 1, data[:i], nil
		}
		return 0, nil, nil // request more data
	}
	if atEOF {
		return len(data), data, nil // final unterminated line
	}
	return 0, nil, nil
}
