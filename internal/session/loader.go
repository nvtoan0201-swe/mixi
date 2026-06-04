package session

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"
)

// maxLineBytes caps a single session line; anything bigger is treated as
// corruption rather than buffered indefinitely.
const maxLineBytes = 10 << 20

// OpenJSONL loads an existing session file: lock (unless read-only),
// stream-scan lines into the index, and recover from a partial trailing
// line left by a crash.
func OpenJSONL(path string, opts Options) (Storage, error) {
	flag := os.O_RDWR | os.O_APPEND
	if opts.ReadOnly {
		flag = os.O_RDONLY
	}
	f, err := os.OpenFile(path, flag, 0)
	if err != nil {
		return nil, fmt.Errorf("session: open: %w", err)
	}
	var lock *fileLock
	if !opts.ReadOnly {
		if lock, err = lockFile(f, path); err != nil {
			f.Close()
			return nil, err
		}
	}
	s := &jsonlStore{path: path, idx: newIndex(), opts: opts, file: f, lock: lock, loaded: true, now: time.Now}
	if err := s.load(); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

// load scans the file line by line. A line that fails to parse is fatal
// when more lines follow (mid-file corruption) but recoverable when it is
// the trailing line without a newline — the signature a kill -9 leaves —
// in which case it is logged, skipped, and truncated away so future
// appends start on a clean line boundary.
func (s *jsonlStore) load() error {
	r := bufio.NewReaderSize(s.file, 256<<10)
	var (
		offset int64 // bytes consumed through the end of the previous line
		lineNo int
	)
	for {
		line, rerr := r.ReadBytes('\n')
		if rerr != nil && rerr != io.EOF {
			return fmt.Errorf("session: read %s: %w", s.path, rerr)
		}
		if len(line) > maxLineBytes {
			return fmt.Errorf("session: %s line %d exceeds %d bytes", s.path, lineNo+1, maxLineBytes)
		}
		if len(line) > 0 {
			lineNo++
			complete := line[len(line)-1] == '\n'
			if perr := s.loadLine(line, lineNo); perr != nil {
				if complete || rerr != io.EOF {
					return perr // corruption with data after it: refuse
				}
				if lineNo == 1 || s.header.Version == 0 {
					// Never truncate a file that isn't provably one of
					// ours: a torn first line means no valid header.
					return perr
				}
				slog.Warn("session: dropping partial trailing line",
					"path", s.path, "line", lineNo, "bytes", len(line))
				if !s.opts.ReadOnly {
					if terr := s.file.Truncate(offset); terr != nil {
						return fmt.Errorf("session: truncate partial tail: %w", terr)
					}
				}
				return nil
			}
			offset += int64(len(line))
			if !complete && rerr == io.EOF && !s.opts.ReadOnly {
				// Valid entry that lost its newline mid-crash: repair so the
				// next append does not glue onto it.
				if _, werr := s.file.Write([]byte("\n")); werr != nil {
					return fmt.Errorf("session: repair missing newline: %w", werr)
				}
			}
		}
		if rerr == io.EOF {
			if lineNo < 1 || s.header.Version == 0 {
				return fmt.Errorf("session: %s has no valid header line", s.path)
			}
			return nil
		}
	}
}

// loadLine parses one line: line 1 must be the header, the rest entries.
func (s *jsonlStore) loadLine(line []byte, lineNo int) error {
	trimmed := bytes.TrimSpace(line)
	if len(trimmed) == 0 {
		return nil // tolerate stray blank lines
	}
	if lineNo == 1 {
		h, err := UnmarshalHeader(trimmed)
		if err != nil {
			return err
		}
		s.header = h
		return nil
	}
	e, err := UnmarshalEntry(trimmed)
	if err != nil {
		return fmt.Errorf("session: %s line %d: %w", s.path, lineNo, err)
	}
	s.idx.add(e)
	return nil
}
