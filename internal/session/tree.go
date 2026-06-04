package session

import (
	"fmt"
	"time"
)

// index is the in-memory view of a session tree, shared by jsonlStore and
// memStore: id lookup, file order, and the replayed current leaf.
type index struct {
	byID    map[string]Entry
	ordered []Entry
	leafID  string
}

func newIndex() *index {
	return &index{byID: map[string]Entry{}}
}

// add registers an entry and replays its effect on the current leaf:
// leaf entries jump to their target; labels and session_info are off-chain
// twigs that never become the position; everything else becomes the leaf.
func (x *index) add(e Entry) {
	x.byID[e.EntryID()] = e
	x.ordered = append(x.ordered, e)
	switch v := e.(type) {
	case *LeafEntry:
		x.leafID = v.TargetID
	case *LabelEntry, *SessionInfoEntry:
	default:
		x.leafID = e.EntryID()
	}
}

func (x *index) get(id string) (Entry, bool) {
	e, ok := x.byID[id]
	return e, ok
}

func (x *index) has(id string) bool {
	_, ok := x.byID[id]
	return ok
}

// pathToRoot walks parentId links from leafID up to the root and returns
// the chain in root-first order. Unknown IDs yield an empty path.
func (x *index) pathToRoot(leafID string) []Entry {
	var rev []Entry
	for id := leafID; id != ""; {
		e, ok := x.byID[id]
		if !ok {
			break
		}
		rev = append(rev, e)
		id = e.ParentID()
	}
	path := make([]Entry, len(rev))
	for i, e := range rev {
		path[len(rev)-1-i] = e
	}
	return path
}

// fillBase assigns identity to an entry about to be appended: a fresh
// collision-checked ID, the current leaf as parent, and a timestamp when
// the caller did not set one.
func (x *index) fillBase(e Entry, now func() time.Time) {
	b := e.base()
	b.ID = newEntryID(x.has)
	b.Parent = x.leafID
	if b.Timestamp == "" {
		b.Timestamp = formatTime(now())
	}
}

// formatTime renders the RFC3339-with-milliseconds UTC form used on every
// entry and header line.
func formatTime(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.000Z")
}

// CommonAncestor returns the deepest entry shared by the paths to oldLeaf
// and newLeaf: the branch point. Empty when the leaves share no history.
func CommonAncestor(s Storage, oldLeaf, newLeaf string) string {
	old := map[string]bool{}
	for _, e := range s.PathToRoot(oldLeaf) {
		old[e.EntryID()] = true
	}
	newPath := s.PathToRoot(newLeaf)
	for i := len(newPath) - 1; i >= 0; i-- {
		if old[newPath[i].EntryID()] {
			return newPath[i].EntryID()
		}
	}
	return ""
}

// validateLeafTarget ensures SetLeaf points at a real on-chain entry.
func (x *index) validateLeafTarget(targetID string) error {
	e, ok := x.byID[targetID]
	if !ok {
		return fmt.Errorf("session: SetLeaf target %q does not exist", targetID)
	}
	switch e.(type) {
	case *LeafEntry, *LabelEntry, *SessionInfoEntry:
		return fmt.Errorf("session: SetLeaf target %q is a %s entry, not a chain position", targetID, e.Type())
	}
	return nil
}
