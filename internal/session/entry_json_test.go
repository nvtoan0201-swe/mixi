package session

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// Round-trip every entry type the golden file does not exercise, plus
// codec edge cases.

func roundTrip(t *testing.T, e Entry) Entry {
	t.Helper()
	line, err := MarshalEntry(e)
	if err != nil {
		t.Fatal(err)
	}
	back, err := UnmarshalEntry(line)
	if err != nil {
		t.Fatalf("unmarshal %s: %v", line, err)
	}
	return back
}

func TestEntryRoundTripAllTypes(t *testing.T) {
	b := Base{ID: "a1b2c3d4", Parent: "00000000", Timestamp: "2026-06-03T06:30:00.000Z"}
	details := &FileLists{ReadFiles: []string{"a.go"}, ModifiedFiles: []string{"b.go"}}
	cases := []Entry{
		&ModelChangeEntry{b, "anthropic", "claude-sonnet-4-6"},
		&ThinkingLevelChangeEntry{b, "high"},
		&ActiveToolsChangeEntry{b, []string{"read", "bash"}},
		&CompactionEntry{b, "did things", "a1b2c3d4", 90000, *details},
		&BranchSummaryEntry{b, "deadbeef", "explored, abandoned", details},
		&CustomEntry{b, "workingset", json.RawMessage(`{"files":{}}`)},
		&CustomMessageEntry{b, "ext", json.RawMessage(`"remember the build flag"`), true, nil, true},
		&LabelEntry{b, "a1b2c3d4", "milestone"},
		&SessionInfoEntry{b, "my session"},
		&LeafEntry{b, "a1b2c3d4"},
	}
	for _, e := range cases {
		back := roundTrip(t, e)
		if !reflect.DeepEqual(back, e) {
			t.Errorf("%s round-trip:\n got %#v\nwant %#v", e.Type(), back, e)
		}
	}
}

func TestRootParentMarshalsAsNull(t *testing.T) {
	e := userEntry("root")
	e.ID = "a1b2c3d4"
	e.Timestamp = "2026-06-03T06:30:00.000Z"
	line, err := MarshalEntry(e)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(line), `"parentId":null`) {
		t.Fatalf("line = %s", line)
	}
	back := roundTrip(t, e)
	if back.ParentID() != "" {
		t.Fatalf("parent = %q, want empty", back.ParentID())
	}
}

func TestUnknownEntryTypeRejected(t *testing.T) {
	_, err := UnmarshalEntry([]byte(`{"type":"hologram","id":"x","parentId":null,"timestamp":"t"}`))
	if err == nil || !strings.Contains(err.Error(), `unknown entry type "hologram"`) {
		t.Fatalf("err = %v", err)
	}
}

func TestHeaderLineWhereEntryExpected(t *testing.T) {
	_, err := UnmarshalEntry([]byte(`{"type":"session","version":1}`))
	if err == nil || !strings.Contains(err.Error(), "header line") {
		t.Fatalf("err = %v", err)
	}
}

func TestHeaderWrongTypeRejected(t *testing.T) {
	_, err := UnmarshalHeader([]byte(`{"type":"message","version":1}`))
	if err == nil || !strings.Contains(err.Error(), `want "session"`) {
		t.Fatalf("err = %v", err)
	}
}
