package session

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Manager owns the on-disk session layout:
// <Root>/<cwd-slug>/<timestamp>_<uuidv7>.jsonl
type Manager struct {
	Root string // sessions root, default ~/.mixi/sessions
	Opts Options
}

// NewManager returns a manager rooted at ~/.mixi/sessions.
func NewManager(opts Options) (*Manager, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("session: resolve home dir: %w", err)
	}
	return &Manager{Root: filepath.Join(home, ".mixi", "sessions"), Opts: opts}, nil
}

// slugify turns an absolute cwd into a flat directory name: leading
// separator stripped, path separators and colons replaced by dashes,
// wrapped in double dashes (e.g. /home/u/proj → --home-u-proj--).
func slugify(cwd string) string {
	s := strings.TrimLeft(cwd, "/\\")
	s = strings.NewReplacer("/", "-", "\\", "-", ":", "-").Replace(s)
	return "--" + s + "--"
}

// Dir returns the sessions directory for a cwd.
func (m *Manager) Dir(cwd string) string {
	return filepath.Join(m.Root, slugify(cwd))
}

func (m *Manager) newPath(cwd string) string {
	name := time.Now().UTC().Format("20060102T150405Z") + "_" + newSessionID() + ".jsonl"
	return filepath.Join(m.Dir(cwd), name)
}

// Create starts a new session for cwd. No file exists until the first
// message entry is appended.
func (m *Manager) Create(cwd string) (Storage, error) {
	return NewJSONL(m.newPath(cwd), Header{CWD: cwd}, m.Opts)
}

// Open loads an existing session file.
func (m *Manager) Open(path string) (Storage, error) {
	return OpenJSONL(path, m.Opts)
}

// ContinueRecent opens the most recently modified session for cwd.
func (m *Manager) ContinueRecent(cwd string) (Storage, error) {
	dir := m.Dir(cwd)
	glob, err := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	if err != nil {
		return nil, err
	}
	var newest string
	var newestMod time.Time
	for _, p := range glob {
		info, err := os.Stat(p)
		if err != nil {
			continue
		}
		if newest == "" || info.ModTime().After(newestMod) {
			newest, newestMod = p, info.ModTime()
		}
	}
	if newest == "" {
		return nil, fmt.Errorf("session: no sessions for %s in %s: %w", cwd, dir, os.ErrNotExist)
	}
	return m.Open(newest)
}

// Fork copies the source file's prefix up to and including entryID into a
// new session whose header records the parent, then positions the leaf at
// the fork point. The source is read without taking its lock.
func (m *Manager) Fork(srcPath, entryID string) (Storage, error) {
	src, err := OpenJSONL(srcPath, Options{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer src.Close()
	if _, ok := src.Get(entryID); !ok {
		return nil, fmt.Errorf("session: fork point %q not found in %s", entryID, srcPath)
	}

	h := src.Header()
	head, err := MarshalHeader(Header{
		Version:       CurrentVersion,
		ID:            newSessionID(),
		Timestamp:     formatTime(time.Now()),
		CWD:           h.CWD,
		ParentSession: srcPath,
	})
	if err != nil {
		return nil, err
	}
	buf := append(head, '\n')
	for _, e := range src.Entries() {
		line, err := MarshalEntry(e)
		if err != nil {
			return nil, err
		}
		buf = append(buf, line...)
		buf = append(buf, '\n')
		if e.EntryID() == entryID {
			break
		}
	}

	dst := m.newPath(h.CWD)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return nil, fmt.Errorf("session: create session dir: %w", err)
	}
	if err := os.WriteFile(dst, buf, 0o600); err != nil {
		return nil, fmt.Errorf("session: write fork: %w", err)
	}
	fork, err := m.Open(dst)
	if err != nil {
		return nil, err
	}
	if fork.LeafID() != entryID {
		if err := fork.SetLeaf(entryID); err != nil {
			fork.Close()
			return nil, err
		}
	}
	return fork, nil
}

// InMemory returns a session that never touches disk (--no-save).
func (m *Manager) InMemory(cwd string) Storage {
	return NewMem(cwd)
}
