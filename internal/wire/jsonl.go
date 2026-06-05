// Package wire frames newline-delimited JSON messages over byte streams.
// It is the shared transport layer for subprocess protocols: MCP servers,
// extension hosts, and RPC mode all speak JSONL over pipes.
package wire

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

// MaxLineBytes caps a single JSONL message. Lines beyond the cap fail the
// read rather than silently truncating, so a misbehaving peer surfaces as
// an explicit error instead of corrupted JSON.
const MaxLineBytes = 1 << 20 // 1 MiB

// ErrLineTooLong reports a message exceeding MaxLineBytes.
var ErrLineTooLong = errors.New("wire: line exceeds 1MiB cap")

// Reader yields one JSONL message per call. Single consumer; not safe for
// concurrent use.
type Reader struct {
	br *bufio.Reader
}

func NewReader(r io.Reader) *Reader {
	return &Reader{br: bufio.NewReaderSize(r, 64<<10)}
}

// ReadLine returns the next newline-terminated message without the
// terminator. io.EOF signals orderly close; a final unterminated line is
// returned with io.EOF on the following call. Empty lines are skipped.
func (r *Reader) ReadLine() ([]byte, error) {
	var buf []byte
	for {
		chunk, isPrefix, err := r.br.ReadLine()
		if err != nil {
			if len(buf) > 0 && err == io.EOF {
				return buf, nil
			}
			return nil, err
		}
		if len(buf)+len(chunk) > MaxLineBytes {
			r.discardLine(isPrefix)
			return nil, ErrLineTooLong
		}
		buf = append(buf, chunk...)
		if isPrefix {
			continue
		}
		if len(buf) == 0 {
			continue // skip blank lines between messages
		}
		return buf, nil
	}
}

// discardLine consumes the remainder of an over-long line so the next read
// starts on a fresh message instead of mid-line garbage.
func (r *Reader) discardLine(isPrefix bool) {
	for isPrefix {
		var err error
		_, isPrefix, err = r.br.ReadLine()
		if err != nil {
			return
		}
	}
}

// deadlineWriter is implemented by net.Conn and friends; pipes to
// subprocesses don't support it, so deadlines are best-effort.
type deadlineWriter interface {
	SetWriteDeadline(t time.Time) error
}

// Writer frames messages as one JSON document per line. Safe for
// concurrent use: each WriteLine is a single atomic write under a mutex.
type Writer struct {
	mu      sync.Mutex
	w       io.Writer
	timeout time.Duration // 0 = no deadline
}

func NewWriter(w io.Writer) *Writer { return &Writer{w: w} }

// SetTimeout arms a per-write deadline. It only takes effect when the
// underlying writer supports SetWriteDeadline; otherwise writes block.
func (w *Writer) SetTimeout(d time.Duration) {
	w.mu.Lock()
	w.timeout = d
	w.mu.Unlock()
}

// WriteLine writes msg followed by a newline as one buffered write. msg
// must be a complete JSON document without embedded raw newlines.
func (w *Writer) WriteLine(msg []byte) error {
	if len(msg) > MaxLineBytes {
		return fmt.Errorf("%w (%d bytes)", ErrLineTooLong, len(msg))
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if dw, ok := w.w.(deadlineWriter); ok && w.timeout > 0 {
		_ = dw.SetWriteDeadline(time.Now().Add(w.timeout))
		defer func() { _ = dw.SetWriteDeadline(time.Time{}) }()
	}
	buf := make([]byte, 0, len(msg)+1)
	buf = append(buf, msg...)
	buf = append(buf, '\n')
	_, err := w.w.Write(buf)
	return err
}
