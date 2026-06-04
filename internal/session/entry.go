// Package session implements append-only JSONL tree storage for agent
// conversations: a version-1 header line followed by entry lines linked by
// parentId chains, with leaf entries tracking the current position so
// branching never destroys history.
package session

import (
	"encoding/json"

	"github.com/user/mixi-agent/internal/ai"
)

// Header is the first line of every session file.
type Header struct {
	Version       int    `json:"version"`
	ID            string `json:"id"`        // full uuidv7
	Timestamp     string `json:"timestamp"` // RFC3339 ms, UTC
	CWD           string `json:"cwd"`
	ParentSession string `json:"parentSession"` // source file path when forked
}

// CurrentVersion is mixi's own schema version (not Pi's). The loader
// rejects files written by a newer version.
const CurrentVersion = 1

// Base carries the fields shared by every entry line.
type Base struct {
	ID        string // 8-hex (uuidv7-derived) or full uuid on collision fallback
	Parent    string // parent entry ID; "" at the root (serialized as null)
	Timestamp string // RFC3339 ms, UTC
}

func (b *Base) EntryID() string   { return b.ID }
func (b *Base) ParentID() string  { return b.Parent }
func (b *Base) EntryTime() string { return b.Timestamp }
func (b *Base) base() *Base       { return b }

// Entry is the sealed union of session line types.
type Entry interface {
	EntryID() string
	ParentID() string
	EntryTime() string
	// Type returns the wire discriminator, e.g. "message".
	Type() string
	base() *Base
}

// FileLists names the files touched by a summarized window.
type FileLists struct {
	ReadFiles     []string `json:"readFiles"`
	ModifiedFiles []string `json:"modifiedFiles"`
}

// MessageEntry holds a provider-level conversation message. Pinned entries
// survive compaction verbatim.
type MessageEntry struct {
	Base
	Message ai.Message
	Pinned  bool
}

// ModelChangeEntry records a model switch mid-session.
type ModelChangeEntry struct {
	Base
	Provider string
	ModelID  string
}

// ThinkingLevelChangeEntry records a thinking-level switch.
type ThinkingLevelChangeEntry struct {
	Base
	ThinkingLevel ai.ThinkingLevel
}

// ActiveToolsChangeEntry records a change to the enabled tool set.
type ActiveToolsChangeEntry struct {
	Base
	ActiveToolNames []string
}

// CompactionEntry replaces summarized history from this point back to
// FirstKeptEntryID's predecessor.
type CompactionEntry struct {
	Base
	Summary          string
	FirstKeptEntryID string
	TokensBefore     int
	Details          FileLists
}

// BranchSummaryEntry carries context from an abandoned branch (FromID is
// the abandoned leaf).
type BranchSummaryEntry struct {
	Base
	FromID  string
	Summary string
	Details *FileLists
}

// CustomEntry stores extension or harness state; never enters LLM context.
type CustomEntry struct {
	Base
	CustomType string
	Data       json.RawMessage
}

// CustomMessageEntry is extension-injected content that IS in LLM context.
// Content is a string or a Text/Image content array; kept raw so unknown
// producers round-trip losslessly.
type CustomMessageEntry struct {
	Base
	CustomType string
	Content    json.RawMessage
	Display    bool
	Details    json.RawMessage
	Pinned     bool
}

// LabelEntry attaches a user label to another entry. Off-chain: nothing
// ever points at a label, so PathToRoot never includes one.
type LabelEntry struct {
	Base
	TargetID string
	Label    string
}

// SessionInfoEntry names the session. Off-chain like labels.
type SessionInfoEntry struct {
	Base
	Name string
}

// LeafEntry moves the current position to TargetID; never in context.
type LeafEntry struct {
	Base
	TargetID string
}

func (*MessageEntry) Type() string             { return "message" }
func (*ModelChangeEntry) Type() string         { return "model_change" }
func (*ThinkingLevelChangeEntry) Type() string { return "thinking_level_change" }
func (*ActiveToolsChangeEntry) Type() string   { return "active_tools_change" }
func (*CompactionEntry) Type() string          { return "compaction" }
func (*BranchSummaryEntry) Type() string       { return "branch_summary" }
func (*CustomEntry) Type() string              { return "custom" }
func (*CustomMessageEntry) Type() string       { return "custom_message" }
func (*LabelEntry) Type() string               { return "label" }
func (*SessionInfoEntry) Type() string         { return "session_info" }
func (*LeafEntry) Type() string                { return "leaf" }
