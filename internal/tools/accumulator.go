package tools

import (
	"fmt"
	"os"
	"sync"
)

// accumulator collects interleaved stdout+stderr from a shell command.
// Memory holds at most a rolling tail (2×MaxBytes); once total output
// exceeds that, the full stream is lazily spilled to a temp file and memory
// keeps only the tail. Safe for concurrent writers (the two pipe copiers).
type accumulator struct {
	mu         sync.Mutex
	maxRolling int
	buf        []byte   // full output until spill, then rolling tail only
	bufStart   int64    // byte offset of buf[0] within the full stream
	total      int64    // bytes written overall
	file       *os.File // nil until spill; holds the full stream afterwards
	fileErr    error    // first spill failure, reported in snapshots
}

func newAccumulator() *accumulator {
	return &accumulator{maxRolling: 2 * MaxBytes}
}

// Write implements io.Writer; both pipes share one accumulator so output
// interleaves in arrival order.
func (a *accumulator) Write(p []byte) (int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.total += int64(len(p))
	a.buf = append(a.buf, p...)
	if a.file != nil && a.fileErr == nil {
		_, a.fileErr = a.file.Write(p)
	}
	if len(a.buf) > a.maxRolling {
		if a.file == nil && a.fileErr == nil {
			a.spillLocked()
		}
		drop := len(a.buf) - a.maxRolling
		a.buf = append(a.buf[:0], a.buf[drop:]...)
		a.bufStart += int64(drop)
	}
	return len(p), nil
}

// spillLocked creates the temp file and seeds it with everything buffered so
// far, so the file always holds the complete stream from byte 0.
func (a *accumulator) spillLocked() {
	f, err := os.CreateTemp("", "mixi-bash-*.log")
	if err != nil {
		a.fileErr = err
		return
	}
	if _, err := f.Write(a.buf); err != nil {
		a.fileErr = err
		f.Close()
		os.Remove(f.Name())
		return
	}
	a.file = f
}

// snapshot describes the stream at one instant.
type snapshot struct {
	Tail     string // rolling tail (≤ maxRolling bytes), rune-boundary safe
	Total    int64  // bytes produced so far
	Spilled  bool
	FilePath string // full-output temp file when Spilled
}

func (a *accumulator) snapshot() snapshot {
	a.mu.Lock()
	defer a.mu.Unlock()
	s := snapshot{Tail: cutUTF8Start(string(a.buf)), Total: a.total}
	if a.file != nil {
		s.Spilled = true
		s.FilePath = a.file.Name()
	}
	return s
}

// readFrom returns the stream from byte offset on, plus the new cursor.
// Pre-spill the memory buffer covers the whole stream; post-spill the temp
// file does. Offsets older than what either holds yield from the oldest
// retained byte.
func (a *accumulator) readFrom(offset int64) (string, int64, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if offset >= a.total {
		return "", a.total, nil
	}
	if a.file != nil {
		buf := make([]byte, a.total-offset)
		n, err := a.file.ReadAt(buf, offset)
		if err != nil {
			return "", offset, fmt.Errorf("read job output: %w", err)
		}
		return string(buf[:n]), a.total, nil
	}
	if offset < a.bufStart {
		offset = a.bufStart
	}
	return string(a.buf[offset-a.bufStart:]), a.total, nil
}

// persistFull guarantees the complete stream exists on disk and returns its
// path: the spill file when one exists, otherwise a temp file written from
// the (still complete) memory buffer. "" when the stream cannot be recovered
// (earlier spill failure).
func (a *accumulator) persistFull() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.file != nil {
		return a.file.Name()
	}
	if a.fileErr != nil || a.bufStart != 0 {
		return ""
	}
	f, err := os.CreateTemp("", "mixi-bash-*.log")
	if err != nil {
		return ""
	}
	defer f.Close()
	if _, err := f.Write(a.buf); err != nil {
		os.Remove(f.Name())
		return ""
	}
	return f.Name()
}

// close releases the spill file handle; the file itself is kept so the
// "[full output: path]" hint stays valid after the command finishes.
func (a *accumulator) close() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.file != nil {
		a.file.Close()
	}
}
