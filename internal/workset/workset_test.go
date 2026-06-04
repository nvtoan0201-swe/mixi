package workset

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestObserveAndLists(t *testing.T) {
	dir := t.TempDir()
	r := filepath.Join(dir, "read.go")
	m := filepath.Join(dir, "mod.go")
	writeFile(t, r, "package a")
	writeFile(t, m, "package b")

	ws := New()
	ws.SetTurn(3)
	ws.ObserveRead(r, []byte("package a"))
	ws.ObserveRead(m, []byte("package b"))
	ws.ObserveWrite(m, []byte("package b"))

	read, written := ws.Lists()
	if len(read) != 2 || read[0] != m || read[1] != r {
		t.Fatalf("read = %v", read)
	}
	if len(written) != 1 || written[0] != m {
		t.Fatalf("written = %v", written)
	}
}

func TestStaleDetection(t *testing.T) {
	dir := t.TempDir()
	fresh := filepath.Join(dir, "fresh.go")
	touched := filepath.Join(dir, "touched.go")
	modified := filepath.Join(dir, "modified.go")
	deleted := filepath.Join(dir, "deleted.go")
	for _, p := range []string{fresh, touched, modified, deleted} {
		writeFile(t, p, "original")
	}

	ws := New()
	ws.SetTurn(2)
	for _, p := range []string{fresh, touched, modified, deleted} {
		ws.ObserveRead(p, []byte("original"))
	}

	// touched: mtime bumped, content identical → hash confirms fresh.
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(touched, future, future); err != nil {
		t.Fatal(err)
	}
	writeFile(t, modified, "changed!!")
	if err := os.Remove(deleted); err != nil {
		t.Fatal(err)
	}

	stale := ws.Stale()
	if len(stale) != 2 {
		t.Fatalf("stale = %+v, want modified+deleted only", stale)
	}
	if stale[0].Path != deleted || stale[0].Reason != "deleted" {
		t.Fatalf("stale[0] = %+v", stale[0])
	}
	if stale[1].Path != modified || stale[1].Reason != "modified" || stale[1].ReadTurn != 2 {
		t.Fatalf("stale[1] = %+v", stale[1])
	}

	// Hash-confirmed file refreshed its stamp: fast path holds now.
	if got := ws.Stale(); len(got) != 2 {
		t.Fatalf("second Stale() = %+v", got)
	}

	// Re-reading the modified file clears its staleness.
	ws.ObserveRead(modified, []byte("changed!!"))
	stale = ws.Stale()
	if len(stale) != 1 || stale[0].Path != deleted {
		t.Fatalf("after re-read, stale = %+v", stale)
	}
}

func TestSnapshotRestoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.go")
	writeFile(t, p, "package a")

	ws := New()
	ws.SetTurn(5)
	ws.ObserveRead(p, []byte("package a"))
	ws.ObserveWrite(p, []byte("package a"))

	raw, changed, err := ws.Snapshot()
	if err != nil || !changed {
		t.Fatalf("snapshot: changed=%v err=%v", changed, err)
	}
	// Snapshot clears dirty: a second one reports no change.
	if _, changed, _ := ws.Snapshot(); changed {
		t.Fatal("second snapshot should report no change")
	}

	restored := New()
	if err := restored.Restore(raw); err != nil {
		t.Fatal(err)
	}
	// The restored set sees the untouched file as fresh...
	if stale := restored.Stale(); len(stale) != 0 {
		t.Fatalf("restored set reports stale: %+v", stale)
	}
	// ...and detects a post-restore modification.
	writeFile(t, p, "package changed")
	stale := restored.Stale()
	if len(stale) != 1 || stale[0].Reason != "modified" || stale[0].ReadTurn != 5 {
		t.Fatalf("stale after restore+modify = %+v", stale)
	}
	read, written := restored.Lists()
	if len(read) != 1 || len(written) != 1 {
		t.Fatalf("restored lists: read=%v written=%v", read, written)
	}
}

func TestRestoreRejectsGarbage(t *testing.T) {
	if err := New().Restore([]byte(`{"files":{"x":{"sha256":"zz","modTime":"bad"}}}`)); err == nil {
		t.Fatal("expected error for malformed snapshot")
	}
}

func TestObserveMissingFileIgnored(t *testing.T) {
	ws := New()
	ws.ObserveRead("/nonexistent/x.go", []byte("data"))
	if read, _ := ws.Lists(); len(read) != 0 {
		t.Fatalf("missing file recorded: %v", read)
	}
}
