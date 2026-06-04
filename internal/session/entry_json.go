package session

import (
	"encoding/json"
	"fmt"

	"github.com/user/mixi-agent/internal/ai"
)

// Wire structs mirror the on-disk shape; parentId uses *string so the root
// entry serializes as an explicit null (Pi-compatible).

type baseJSON struct {
	Type      string  `json:"type"`
	ID        string  `json:"id"`
	ParentID  *string `json:"parentId"`
	Timestamp string  `json:"timestamp"`
}

func (w baseJSON) toBase() Base {
	b := Base{ID: w.ID, Timestamp: w.Timestamp}
	if w.ParentID != nil {
		b.Parent = *w.ParentID
	}
	return b
}

func wireBase(e Entry) baseJSON {
	b := e.base()
	w := baseJSON{Type: e.Type(), ID: b.ID, Timestamp: b.Timestamp}
	if b.Parent != "" {
		w.ParentID = &b.Parent
	}
	return w
}

type messageJSON struct {
	baseJSON
	Message json.RawMessage `json:"message"`
	Pinned  bool            `json:"pinned,omitempty"`
}
type modelChangeJSON struct {
	baseJSON
	Provider string `json:"provider"`
	ModelID  string `json:"modelId"`
}
type thinkingLevelJSON struct {
	baseJSON
	ThinkingLevel ai.ThinkingLevel `json:"thinkingLevel"`
}
type activeToolsJSON struct {
	baseJSON
	ActiveToolNames []string `json:"activeToolNames"`
}
type compactionJSON struct {
	baseJSON
	Summary          string    `json:"summary"`
	FirstKeptEntryID string    `json:"firstKeptEntryId"`
	TokensBefore     int       `json:"tokensBefore"`
	Details          FileLists `json:"details"`
}
type branchSummaryJSON struct {
	baseJSON
	FromID  string     `json:"fromId"`
	Summary string     `json:"summary"`
	Details *FileLists `json:"details,omitempty"`
}
type customJSON struct {
	baseJSON
	CustomType string          `json:"customType"`
	Data       json.RawMessage `json:"data,omitempty"`
}
type customMessageJSON struct {
	baseJSON
	CustomType string          `json:"customType"`
	Content    json.RawMessage `json:"content"`
	Display    bool            `json:"display"`
	Details    json.RawMessage `json:"details,omitempty"`
	Pinned     bool            `json:"pinned,omitempty"`
}
type labelJSON struct {
	baseJSON
	TargetID string `json:"targetId"`
	Label    string `json:"label,omitempty"`
}
type sessionInfoJSON struct {
	baseJSON
	Name string `json:"name,omitempty"`
}
type leafJSON struct {
	baseJSON
	TargetID string `json:"targetId"`
}

// MarshalEntry serializes one entry as a single JSONL line (no newline).
func MarshalEntry(e Entry) ([]byte, error) {
	w := wireBase(e)
	switch v := e.(type) {
	case *MessageEntry:
		msg, err := ai.MarshalMessage(v.Message)
		if err != nil {
			return nil, fmt.Errorf("session: marshal message entry %s: %w", v.ID, err)
		}
		return json.Marshal(messageJSON{w, msg, v.Pinned})
	case *ModelChangeEntry:
		return json.Marshal(modelChangeJSON{w, v.Provider, v.ModelID})
	case *ThinkingLevelChangeEntry:
		return json.Marshal(thinkingLevelJSON{w, v.ThinkingLevel})
	case *ActiveToolsChangeEntry:
		return json.Marshal(activeToolsJSON{w, v.ActiveToolNames})
	case *CompactionEntry:
		return json.Marshal(compactionJSON{w, v.Summary, v.FirstKeptEntryID, v.TokensBefore, v.Details})
	case *BranchSummaryEntry:
		return json.Marshal(branchSummaryJSON{w, v.FromID, v.Summary, v.Details})
	case *CustomEntry:
		return json.Marshal(customJSON{w, v.CustomType, v.Data})
	case *CustomMessageEntry:
		return json.Marshal(customMessageJSON{w, v.CustomType, v.Content, v.Display, v.Details, v.Pinned})
	case *LabelEntry:
		return json.Marshal(labelJSON{w, v.TargetID, v.Label})
	case *SessionInfoEntry:
		return json.Marshal(sessionInfoJSON{w, v.Name})
	case *LeafEntry:
		return json.Marshal(leafJSON{w, v.TargetID})
	default:
		return nil, fmt.Errorf("session: cannot marshal unknown entry type %T", e)
	}
}

// UnmarshalEntry parses one JSONL line into its concrete entry type by
// dispatching on the "type" discriminator.
func UnmarshalEntry(line []byte) (Entry, error) {
	var probe struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(line, &probe); err != nil {
		return nil, fmt.Errorf("session: invalid entry JSON: %w", err)
	}
	switch probe.Type {
	case "message":
		var w messageJSON
		if err := json.Unmarshal(line, &w); err != nil {
			return nil, err
		}
		msg, err := ai.UnmarshalMessage(w.Message)
		if err != nil {
			return nil, fmt.Errorf("session: entry %s: %w", w.ID, err)
		}
		return &MessageEntry{w.toBase(), msg, w.Pinned}, nil
	case "model_change":
		var w modelChangeJSON
		if err := json.Unmarshal(line, &w); err != nil {
			return nil, err
		}
		return &ModelChangeEntry{w.toBase(), w.Provider, w.ModelID}, nil
	case "thinking_level_change":
		var w thinkingLevelJSON
		if err := json.Unmarshal(line, &w); err != nil {
			return nil, err
		}
		return &ThinkingLevelChangeEntry{w.toBase(), w.ThinkingLevel}, nil
	case "active_tools_change":
		var w activeToolsJSON
		if err := json.Unmarshal(line, &w); err != nil {
			return nil, err
		}
		return &ActiveToolsChangeEntry{w.toBase(), w.ActiveToolNames}, nil
	case "compaction":
		var w compactionJSON
		if err := json.Unmarshal(line, &w); err != nil {
			return nil, err
		}
		return &CompactionEntry{w.toBase(), w.Summary, w.FirstKeptEntryID, w.TokensBefore, w.Details}, nil
	case "branch_summary":
		var w branchSummaryJSON
		if err := json.Unmarshal(line, &w); err != nil {
			return nil, err
		}
		return &BranchSummaryEntry{w.toBase(), w.FromID, w.Summary, w.Details}, nil
	case "custom":
		var w customJSON
		if err := json.Unmarshal(line, &w); err != nil {
			return nil, err
		}
		return &CustomEntry{w.toBase(), w.CustomType, w.Data}, nil
	case "custom_message":
		var w customMessageJSON
		if err := json.Unmarshal(line, &w); err != nil {
			return nil, err
		}
		return &CustomMessageEntry{w.toBase(), w.CustomType, w.Content, w.Display, w.Details, w.Pinned}, nil
	case "label":
		var w labelJSON
		if err := json.Unmarshal(line, &w); err != nil {
			return nil, err
		}
		return &LabelEntry{w.toBase(), w.TargetID, w.Label}, nil
	case "session_info":
		var w sessionInfoJSON
		if err := json.Unmarshal(line, &w); err != nil {
			return nil, err
		}
		return &SessionInfoEntry{w.toBase(), w.Name}, nil
	case "leaf":
		var w leafJSON
		if err := json.Unmarshal(line, &w); err != nil {
			return nil, err
		}
		return &LeafEntry{w.toBase(), w.TargetID}, nil
	case "session":
		return nil, fmt.Errorf("session: header line found where an entry was expected")
	default:
		return nil, fmt.Errorf("session: unknown entry type %q", probe.Type)
	}
}

type headerJSON struct {
	Type string `json:"type"`
	Header
}

// MarshalHeader serializes the header line.
func MarshalHeader(h Header) ([]byte, error) {
	return json.Marshal(headerJSON{"session", h})
}

// UnmarshalHeader parses line 1 of a session file, rejecting files written
// by a newer mixi.
func UnmarshalHeader(line []byte) (Header, error) {
	var w headerJSON
	if err := json.Unmarshal(line, &w); err != nil {
		return Header{}, fmt.Errorf("session: invalid header line: %w", err)
	}
	if w.Type != "session" {
		return Header{}, fmt.Errorf("session: first line has type %q, want \"session\"", w.Type)
	}
	if w.Version > CurrentVersion {
		return Header{}, fmt.Errorf("session: file version %d is newer than supported version %d; upgrade mixi to open it", w.Version, CurrentVersion)
	}
	return w.Header, nil
}
