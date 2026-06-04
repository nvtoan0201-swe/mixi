package session

// Storage is the session persistence seam: jsonlStore on disk, memStore
// for tests and --no-save mode.
type Storage interface {
	Header() Header
	// Append assigns ID, parent (current leaf) and timestamp, then persists
	// the entry. Identity fields set by the caller are overwritten.
	Append(e Entry) error
	Get(id string) (Entry, bool)
	// PathToRoot returns the parentId chain from leafID to the root,
	// root-first.
	PathToRoot(leafID string) []Entry
	LeafID() string
	// SetLeaf appends a leaf entry moving the current position to targetID.
	SetLeaf(targetID string) error
	// Entries returns every entry in file order (for /tree UI).
	Entries() []Entry
	// Close releases the file lock; the Storage is unusable afterwards.
	Close() error
}

// Options tunes disk-backed stores.
type Options struct {
	// Fsync syncs the file after each message entry (cheap entries like
	// leaf/label ride along on the next sync).
	Fsync bool
	// ReadOnly opens without the advisory lock, for replay and inspection.
	ReadOnly bool
}
