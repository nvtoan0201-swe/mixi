package session

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

// The design-of-record 12-line example session: a linear conversation, a
// custom workingset entry, a label, an abandoned branch navigated away
// from via a leaf entry, and a branch summary at the new position.
const goldenPath = "testdata/example_session.jsonl"

func TestGoldenParsesToExpectedTree(t *testing.T) {
	s, err := OpenJSONL(goldenPath, Options{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	h := s.Header()
	if h.Version != 1 || h.CWD != "/home/u/proj" || h.ID != "0190a1b2-7c3d-7e4f-8a5b-6c7d8e9f0a1b" {
		t.Fatalf("header = %+v", h)
	}
	if n := len(s.Entries()); n != 11 {
		t.Fatalf("entries = %d, want 11", n)
	}

	// The trailing branch_summary chains from f6a7b8c9 (where the leaf
	// entry navigated to) and becomes the final position.
	if got := s.LeafID(); got != "e1f2a3b4" {
		t.Fatalf("leaf = %q, want e1f2a3b4", got)
	}

	wantPath := []string{"a1b2c3d4", "b2c3d4e5", "c3d4e5f6", "d4e5f6a7", "e5f6a7b8", "f6a7b8c9", "e1f2a3b4"}
	var gotPath []string
	for _, e := range s.PathToRoot(s.LeafID()) {
		gotPath = append(gotPath, e.EntryID())
	}
	if !reflect.DeepEqual(gotPath, wantPath) {
		t.Fatalf("path = %v\nwant  %v", gotPath, wantPath)
	}

	// The abandoned branch is intact and reachable; its path goes through
	// the custom entry, not the label (labels are off-chain).
	wantOld := []string{"a1b2c3d4", "b2c3d4e5", "c3d4e5f6", "d4e5f6a7", "e5f6a7b8", "f6a7b8c9", "a7b8c9d0", "c9d0e1f2"}
	gotPath = nil
	for _, e := range s.PathToRoot("c9d0e1f2") {
		gotPath = append(gotPath, e.EntryID())
	}
	if !reflect.DeepEqual(gotPath, wantOld) {
		t.Fatalf("old path = %v\nwant     %v", gotPath, wantOld)
	}

	if got := CommonAncestor(s, "c9d0e1f2", s.LeafID()); got != "f6a7b8c9" {
		t.Fatalf("common ancestor = %q, want f6a7b8c9", got)
	}

	// Spot-check concrete decode of the branch summary.
	e, ok := s.Get("e1f2a3b4")
	if !ok {
		t.Fatal("branch summary missing")
	}
	bs := e.(*BranchSummaryEntry)
	if bs.FromID != "c9d0e1f2" || bs.Summary == "" || bs.Details == nil {
		t.Fatalf("branch summary = %+v", bs)
	}
}

// TestGoldenRoundTrip re-marshals every golden line and requires semantic
// JSON equality: no field lost, none invented.
func TestGoldenRoundTrip(t *testing.T) {
	f, err := os.Open(goldenPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := bytes.TrimSpace(sc.Bytes())
		var out []byte
		if lineNo == 1 {
			h, err := UnmarshalHeader(line)
			if err != nil {
				t.Fatalf("line %d: %v", lineNo, err)
			}
			out, err = MarshalHeader(h)
			if err != nil {
				t.Fatalf("line %d: %v", lineNo, err)
			}
		} else {
			e, err := UnmarshalEntry(line)
			if err != nil {
				t.Fatalf("line %d: %v", lineNo, err)
			}
			out, err = MarshalEntry(e)
			if err != nil {
				t.Fatalf("line %d: %v", lineNo, err)
			}
		}
		var want, got any
		if err := json.Unmarshal(line, &want); err != nil {
			t.Fatalf("line %d: %v", lineNo, err)
		}
		if err := json.Unmarshal(out, &got); err != nil {
			t.Fatalf("line %d remarshal: %v", lineNo, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("line %d round-trip mismatch\n got: %s\nwant: %s", lineNo, out, line)
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	if lineNo != 12 {
		t.Fatalf("golden file has %d lines, want 12", lineNo)
	}
}
