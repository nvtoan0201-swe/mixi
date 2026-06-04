package session

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// jsonlStore is the disk-backed Storage: one JSONL file, header first,
// append-only entries. New sessions defer the first write — nothing
// touches disk until the first message entry — so abandoned empty
// sessions never litter the sessions directory.
type jsonlStore struct {
	mu      sync.Mutex
	path    string
	header  Header
	idx     *index
	opts    Options
	file    *os.File  // nil until the deferred first write happens
	lock    *fileLock // nil when read-only
	pending [][]byte  // marshaled lines buffered before the first message
	loaded  bool      // opened from an existing file (file != nil immediately)
	closed  bool
	now     func() time.Time
}

// NewJSONL creates a new disk-backed session at path. The file is not
// created until the first message entry is appended.
func NewJSONL(path string, h Header, opts Options) (Storage, error) {
	if opts.ReadOnly {
		return nil, errors.New("session: cannot create a new session read-only")
	}
	if h.Version == 0 {
		h.Version = CurrentVersion
	}
	if h.ID == "" {
		h.ID = newSessionID()
	}
	if h.Timestamp == "" {
		h.Timestamp = formatTime(time.Now())
	}
	return &jsonlStore{path: path, header: h, idx: newIndex(), opts: opts, now: time.Now}, nil
}

// Header needs no mutex: it is written only before the store is shared
// (NewJSONL or load) and immutable afterwards.
func (s *jsonlStore) Header() Header { return s.header }

func (s *jsonlStore) Append(e Entry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("session: store is closed")
	}
	if s.opts.ReadOnly {
		return errors.New("session: store is read-only")
	}
	s.idx.fillBase(e, s.now)
	line, err := MarshalEntry(e)
	if err != nil {
		return err
	}

	// Persist before indexing: if the write fails, the in-memory tree must
	// not advance past what disk holds, or later appends would chain off
	// an entry a reload will never see.
	_, isMsg := e.(*MessageEntry)
	switch {
	case s.file == nil && !isMsg:
		s.pending = append(s.pending, line) // still deferred: no message yet
	case s.file == nil:
		s.pending = append(s.pending, line)
		if err := s.createAndFlush(); err != nil {
			s.pending = s.pending[:len(s.pending)-1]
			return err
		}
	default:
		if err := s.writeLine(line); err != nil {
			return err
		}
	}
	s.idx.add(e)
	if isMsg && s.opts.Fsync && s.file != nil {
		return s.file.Sync()
	}
	return nil
}

// createAndFlush performs the deferred first write: create the file
// exclusively, take the lock, and write header plus everything buffered
// in one call.
func (s *jsonlStore) createAndFlush() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("session: create session dir: %w", err)
	}
	f, err := os.OpenFile(s.path, os.O_CREATE|os.O_EXCL|os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("session: create session file: %w", err)
	}
	lock, err := lockFile(f, s.path)
	if err != nil {
		f.Close()
		return err
	}
	head, err := MarshalHeader(s.header)
	if err != nil {
		lock.release()
		f.Close()
		return err
	}
	buf := append(head, '\n')
	for _, l := range s.pending {
		buf = append(buf, l...)
		buf = append(buf, '\n')
	}
	if _, err := f.Write(buf); err != nil {
		lock.release()
		f.Close()
		os.Remove(s.path) // leave no torn file behind; a retry recreates it
		return fmt.Errorf("session: first write: %w", err)
	}
	s.file, s.lock, s.pending = f, lock, nil
	if s.opts.Fsync {
		return s.file.Sync()
	}
	return nil
}

func (s *jsonlStore) writeLine(line []byte) error {
	if _, err := s.file.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("session: append: %w", err)
	}
	return nil
}

func (s *jsonlStore) Get(id string) (Entry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.idx.get(id)
}

func (s *jsonlStore) PathToRoot(leafID string) []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.idx.pathToRoot(leafID)
}

func (s *jsonlStore) LeafID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.idx.leafID
}

func (s *jsonlStore) SetLeaf(targetID string) error {
	s.mu.Lock()
	err := s.idx.validateLeafTarget(targetID)
	s.mu.Unlock()
	if err != nil {
		return err
	}
	return s.Append(&LeafEntry{TargetID: targetID})
}

func (s *jsonlStore) Entries() []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Entry, len(s.idx.ordered))
	copy(out, s.idx.ordered)
	return out
}

// Path returns the on-disk location (which may not exist yet for a
// deferred new session).
func (s *jsonlStore) Path() string { return s.path }

func (s *jsonlStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if s.lock != nil {
		s.lock.release()
		s.lock = nil
	}
	if s.file != nil {
		err := s.file.Close()
		s.file = nil
		return err
	}
	return nil
}
