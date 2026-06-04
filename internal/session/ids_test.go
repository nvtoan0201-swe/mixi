package session

import (
	"regexp"
	"testing"
)

var hex8 = regexp.MustCompile(`^[0-9a-f]{8}$`)

func TestNewEntryIDShortForm(t *testing.T) {
	id := newEntryID(func(string) bool { return false })
	if !hex8.MatchString(id) {
		t.Fatalf("id = %q, want 8 hex chars", id)
	}
}

// Rapid generation must not collide: this is exactly why the ID slices the
// random tail of the uuidv7 rather than its timestamp prefix.
func TestNewEntryIDUniqueInTightLoop(t *testing.T) {
	seen := map[string]bool{}
	taken := func(id string) bool { return seen[id] }
	for i := 0; i < 1000; i++ {
		id := newEntryID(taken)
		if seen[id] {
			t.Fatalf("collision at %d: %q", i, id)
		}
		if !hex8.MatchString(id) {
			t.Fatalf("fell back to full uuid at %d: %q", i, id)
		}
		seen[id] = true
	}
}

func TestNewEntryIDCollisionFallback(t *testing.T) {
	id := newEntryID(func(string) bool { return true }) // everything taken
	if len(id) != 36 {
		t.Fatalf("fallback id = %q, want full uuid", id)
	}
}
