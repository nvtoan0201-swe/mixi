package session

import (
	"errors"
	"sync"
	"time"
)

// memStore keeps a session entirely in memory: tests and --no-save mode.
// Same tree semantics as jsonlStore, no disk, no lock.
type memStore struct {
	mu     sync.Mutex
	header Header
	idx    *index
	closed bool
	now    func() time.Time
}

// NewMem creates an in-memory session.
func NewMem(cwd string) Storage {
	return &memStore{
		header: Header{Version: CurrentVersion, ID: newSessionID(), Timestamp: formatTime(time.Now()), CWD: cwd},
		idx:    newIndex(),
		now:    time.Now,
	}
}

func (s *memStore) Header() Header { return s.header }

func (s *memStore) Append(e Entry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("session: store is closed")
	}
	s.idx.fillBase(e, s.now)
	// Marshal even though nothing is written: surfaces unserializable
	// entries with the same timing as the disk store.
	if _, err := MarshalEntry(e); err != nil {
		return err
	}
	s.idx.add(e)
	return nil
}

func (s *memStore) Get(id string) (Entry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.idx.get(id)
}

func (s *memStore) PathToRoot(leafID string) []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.idx.pathToRoot(leafID)
}

func (s *memStore) LeafID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.idx.leafID
}

func (s *memStore) SetLeaf(targetID string) error {
	s.mu.Lock()
	err := s.idx.validateLeafTarget(targetID)
	s.mu.Unlock()
	if err != nil {
		return err
	}
	return s.Append(&LeafEntry{TargetID: targetID})
}

func (s *memStore) Entries() []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Entry, len(s.idx.ordered))
	copy(out, s.idx.ordered)
	return out
}

func (s *memStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}
