package tools

import (
	"path/filepath"
	"sync"
)

// mutQueue serializes mutations to the same file while letting different
// files proceed in parallel. Keys are realpaths so hardlink/symlink aliases
// of one file share a lock; entries are refcounted and removed when idle.
type mutQueue struct {
	mu    sync.Mutex
	locks map[string]*mutEntry
}

type mutEntry struct {
	mu   sync.Mutex
	refs int
}

func newMutQueue() *mutQueue {
	return &mutQueue{locks: map[string]*mutEntry{}}
}

// lock acquires the per-file lock for path and returns its release func.
func (q *mutQueue) lock(path string) func() {
	key := mutKey(path)
	q.mu.Lock()
	e := q.locks[key]
	if e == nil {
		e = &mutEntry{}
		q.locks[key] = e
	}
	e.refs++
	q.mu.Unlock()

	e.mu.Lock()
	return func() {
		e.mu.Unlock()
		q.mu.Lock()
		e.refs--
		if e.refs == 0 {
			delete(q.locks, key)
		}
		q.mu.Unlock()
	}
}

// mutKey resolves symlinks so two paths naming one inode serialize; for
// not-yet-existing files (write creates them) it falls back to the cleaned
// absolute path, resolving the parent directory when possible.
func mutKey(path string) string {
	if rp, err := filepath.EvalSymlinks(path); err == nil {
		return rp
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	if dir, err := filepath.EvalSymlinks(filepath.Dir(abs)); err == nil {
		return filepath.Join(dir, filepath.Base(abs))
	}
	return abs
}
