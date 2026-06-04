package session

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/user/mixi-agent/internal/ai"
)

// The conformance suite runs identically against both Storage
// implementations: tree semantics must not depend on the backing medium.

func userEntry(text string) *MessageEntry {
	return &MessageEntry{Message: ai.UserMessage{
		Content:   []ai.Content{ai.TextContent{Text: text}},
		Timestamp: 1780470601120,
	}}
}

func forEachStore(t *testing.T, fn func(t *testing.T, s Storage)) {
	t.Run("jsonl", func(t *testing.T) {
		s, err := NewJSONL(filepath.Join(t.TempDir(), "s.jsonl"), Header{CWD: "/tmp/x"}, Options{})
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		fn(t, s)
	})
	t.Run("mem", func(t *testing.T) {
		s := NewMem("/tmp/x")
		defer s.Close()
		fn(t, s)
	})
}

func TestConformanceAppendChainsFromLeaf(t *testing.T) {
	forEachStore(t, func(t *testing.T, s Storage) {
		a, b := userEntry("one"), userEntry("two")
		if err := s.Append(a); err != nil {
			t.Fatal(err)
		}
		if a.ParentID() != "" || a.EntryID() == "" || a.EntryTime() == "" {
			t.Fatalf("root entry base = %+v", a.Base)
		}
		if err := s.Append(b); err != nil {
			t.Fatal(err)
		}
		if b.ParentID() != a.EntryID() {
			t.Fatalf("parent = %q, want %q", b.ParentID(), a.EntryID())
		}
		if s.LeafID() != b.EntryID() {
			t.Fatalf("leaf = %q, want %q", s.LeafID(), b.EntryID())
		}
		got, ok := s.Get(a.EntryID())
		if !ok || got.(*MessageEntry).Message.(ai.UserMessage).Content[0].(ai.TextContent).Text != "one" {
			t.Fatalf("Get(%q) = %v, %v", a.EntryID(), got, ok)
		}
		path := s.PathToRoot(s.LeafID())
		if len(path) != 2 || path[0].EntryID() != a.EntryID() || path[1].EntryID() != b.EntryID() {
			t.Fatalf("path = %v", path)
		}
	})
}

func TestConformanceOffChainEntriesDoNotMoveLeaf(t *testing.T) {
	forEachStore(t, func(t *testing.T, s Storage) {
		a := userEntry("anchor")
		if err := s.Append(a); err != nil {
			t.Fatal(err)
		}
		if err := s.Append(&LabelEntry{TargetID: a.EntryID(), Label: "milestone"}); err != nil {
			t.Fatal(err)
		}
		if err := s.Append(&SessionInfoEntry{Name: "my session"}); err != nil {
			t.Fatal(err)
		}
		if s.LeafID() != a.EntryID() {
			t.Fatalf("leaf moved to %q by off-chain entries", s.LeafID())
		}
		// On-chain entry types do move it.
		mc := &ModelChangeEntry{Provider: "anthropic", ModelID: "claude-sonnet-4-6"}
		if err := s.Append(mc); err != nil {
			t.Fatal(err)
		}
		if s.LeafID() != mc.EntryID() {
			t.Fatalf("leaf = %q, want model_change %q", s.LeafID(), mc.EntryID())
		}
	})
}

func TestConformanceSetLeafBranches(t *testing.T) {
	forEachStore(t, func(t *testing.T, s Storage) {
		a, b := userEntry("trunk"), userEntry("abandoned")
		for _, e := range []Entry{a, b} {
			if err := s.Append(e); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.SetLeaf(a.EntryID()); err != nil {
			t.Fatal(err)
		}
		if s.LeafID() != a.EntryID() {
			t.Fatalf("leaf = %q after SetLeaf", s.LeafID())
		}
		c := userEntry("new branch")
		if err := s.Append(c); err != nil {
			t.Fatal(err)
		}
		if c.ParentID() != a.EntryID() {
			t.Fatalf("branch parent = %q, want %q", c.ParentID(), a.EntryID())
		}
		if got := CommonAncestor(s, b.EntryID(), c.EntryID()); got != a.EntryID() {
			t.Fatalf("common ancestor = %q, want %q", got, a.EntryID())
		}
		// Abandoned branch is still readable.
		if _, ok := s.Get(b.EntryID()); !ok {
			t.Fatal("abandoned entry vanished")
		}
	})
}

func TestConformanceSetLeafValidation(t *testing.T) {
	forEachStore(t, func(t *testing.T, s Storage) {
		a := userEntry("x")
		if err := s.Append(a); err != nil {
			t.Fatal(err)
		}
		if err := s.SetLeaf("deadbeef"); err == nil || !strings.Contains(err.Error(), "does not exist") {
			t.Fatalf("err = %v", err)
		}
		if err := s.Append(&LabelEntry{TargetID: a.EntryID(), Label: "l"}); err != nil {
			t.Fatal(err)
		}
		labelID := s.Entries()[len(s.Entries())-1].EntryID()
		if err := s.SetLeaf(labelID); err == nil || !strings.Contains(err.Error(), "not a chain position") {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestConformanceClosedStoreRejectsAppend(t *testing.T) {
	forEachStore(t, func(t *testing.T, s Storage) {
		if err := s.Append(userEntry("x")); err != nil {
			t.Fatal(err)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		if err := s.Append(userEntry("y")); err == nil {
			t.Fatal("append after Close succeeded")
		}
	})
}

func TestConformanceEntryIDsAreUnique(t *testing.T) {
	forEachStore(t, func(t *testing.T, s Storage) {
		seen := map[string]bool{}
		for i := 0; i < 50; i++ {
			e := userEntry("m")
			if err := s.Append(e); err != nil {
				t.Fatal(err)
			}
			if seen[e.EntryID()] {
				t.Fatalf("duplicate id %q at %d", e.EntryID(), i)
			}
			seen[e.EntryID()] = true
		}
	})
}
