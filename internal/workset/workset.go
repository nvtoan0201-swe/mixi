// Package workset tracks the files an agent session has touched and builds
// the per-request LLM context: compaction summary, pinned entries, kept
// history, and a staleness notice for files changed outside the agent.
package workset

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"sync"
	"time"
)

// FileStamp records what the agent last saw of one file.
type FileStamp struct {
	LastReadTurn  int       // turn of most recent read (0 = never read)
	LastWriteTurn int       // turn of most recent agent write/edit
	ModTime       time.Time // mtime observed at last read/write
	Size          int64
	SHA256        [32]byte // content hash at last read/write
}

// StaleFile names a tracked file that changed outside the agent.
type StaleFile struct {
	Path     string
	ReadTurn int    // last turn the agent saw it
	Reason   string // "modified" | "deleted"
}

// WorkingSet tracks files the agent has touched this session. Methods are
// safe for concurrent use: parallel tools observe from multiple goroutines.
type WorkingSet struct {
	mu    sync.Mutex
	turn  int
	files map[string]FileStamp // key: absolute cleaned path
	dirty bool
}

func New() *WorkingSet {
	return &WorkingSet{files: map[string]FileStamp{}}
}

// SetTurn updates the turn number stamped on subsequent observations.
func (w *WorkingSet) SetTurn(n int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.turn = n
}

// ObserveRead records that the agent read path and saw data. Implements the
// tools file observer; path must already be absolute and cleaned.
func (w *WorkingSet) ObserveRead(path string, data []byte) { w.observe(path, data, false) }

// ObserveWrite records that the agent wrote data to path.
func (w *WorkingSet) ObserveWrite(path string, data []byte) { w.observe(path, data, true) }

func (w *WorkingSet) observe(path string, data []byte, write bool) {
	st, err := os.Stat(path)
	if err != nil {
		return // raced with deletion; next observation re-records
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	s := w.files[path]
	if write {
		s.LastWriteTurn = w.turn
	} else {
		s.LastReadTurn = w.turn
	}
	s.ModTime = st.ModTime()
	s.Size = st.Size()
	s.SHA256 = sha256.Sum256(data)
	w.files[path] = s
	w.dirty = true
}

// Stale returns tracked files whose on-disk state no longer matches the
// recorded stamp: changed by the user, git, or a build — anything but the
// agent. The mtime+size check is the fast path; on mismatch the content hash
// decides, so a touch(1) alone does not flag a file.
func (w *WorkingSet) Stale() []StaleFile {
	w.mu.Lock()
	paths := make([]string, 0, len(w.files))
	for p := range w.files {
		paths = append(paths, p)
	}
	stamps := make(map[string]FileStamp, len(w.files))
	for p, s := range w.files {
		stamps[p] = s
	}
	w.mu.Unlock()
	sort.Strings(paths)

	var out []StaleFile
	for _, p := range paths {
		s := stamps[p]
		st, err := os.Stat(p)
		if err != nil {
			out = append(out, StaleFile{Path: p, ReadTurn: s.LastReadTurn, Reason: "deleted"})
			continue
		}
		if st.ModTime().Equal(s.ModTime) && st.Size() == s.Size {
			continue
		}
		data, err := os.ReadFile(p)
		if err != nil {
			out = append(out, StaleFile{Path: p, ReadTurn: s.LastReadTurn, Reason: "deleted"})
			continue
		}
		if sha256.Sum256(data) == s.SHA256 {
			// Content unchanged; refresh the stamp so the fast path holds.
			w.mu.Lock()
			if cur, ok := w.files[p]; ok && cur.SHA256 == s.SHA256 {
				cur.ModTime, cur.Size = st.ModTime(), st.Size()
				w.files[p] = cur
			}
			w.mu.Unlock()
			continue
		}
		out = append(out, StaleFile{Path: p, ReadTurn: s.LastReadTurn, Reason: "modified"})
	}
	return out
}

// Lists returns the read and written path sets for compaction file lists.
func (w *WorkingSet) Lists() (read, written []string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for p, s := range w.files {
		if s.LastReadTurn > 0 {
			read = append(read, p)
		}
		if s.LastWriteTurn > 0 {
			written = append(written, p)
		}
	}
	sort.Strings(read)
	sort.Strings(written)
	return read, written
}

// ── persistence (custom session entry, customType "workingset") ────────────

type stampJSON struct {
	LastReadTurn  int    `json:"lastReadTurn,omitempty"`
	LastWriteTurn int    `json:"lastWriteTurn,omitempty"`
	ModTime       string `json:"modTime"`
	Size          int64  `json:"size"`
	SHA256        string `json:"sha256"`
}

type stateJSON struct {
	Turn  int                  `json:"turn"`
	Files map[string]stampJSON `json:"files"`
}

// Snapshot serializes the set for persistence and clears the dirty flag.
// Second return reports whether anything changed since the last snapshot.
func (w *WorkingSet) Snapshot() (json.RawMessage, bool, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.dirty {
		return nil, false, nil
	}
	st := stateJSON{Turn: w.turn, Files: make(map[string]stampJSON, len(w.files))}
	for p, s := range w.files {
		st.Files[p] = stampJSON{
			LastReadTurn:  s.LastReadTurn,
			LastWriteTurn: s.LastWriteTurn,
			ModTime:       s.ModTime.UTC().Format(time.RFC3339Nano),
			Size:          s.Size,
			SHA256:        hex.EncodeToString(s.SHA256[:]),
		}
	}
	raw, err := json.Marshal(st)
	if err != nil {
		return nil, false, err
	}
	w.dirty = false
	return raw, true, nil
}

// Restore replaces the set from a persisted snapshot (session resume).
func (w *WorkingSet) Restore(raw json.RawMessage) error {
	var st stateJSON
	if err := json.Unmarshal(raw, &st); err != nil {
		return fmt.Errorf("workset: restore: %w", err)
	}
	files := make(map[string]FileStamp, len(st.Files))
	for p, s := range st.Files {
		mod, err := time.Parse(time.RFC3339Nano, s.ModTime)
		if err != nil {
			return fmt.Errorf("workset: restore %s: %w", p, err)
		}
		sum, err := hex.DecodeString(s.SHA256)
		if err != nil || len(sum) != sha256.Size {
			return fmt.Errorf("workset: restore %s: bad sha256", p)
		}
		fs := FileStamp{
			LastReadTurn:  s.LastReadTurn,
			LastWriteTurn: s.LastWriteTurn,
			ModTime:       mod,
			Size:          s.Size,
		}
		copy(fs.SHA256[:], sum)
		files[p] = fs
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.turn = st.Turn
	w.files = files
	w.dirty = false
	return nil
}
