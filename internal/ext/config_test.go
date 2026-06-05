package ext

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestParseConfigs(t *testing.T) {
	t.Setenv("EXT_SECRET", "s3cret")
	raw := map[string]json.RawMessage{
		"gate": json.RawMessage(`{"command":"/bin/gate","args":["-v"],"env":{"TOKEN":"${EXT_SECRET}"}}`),
	}
	cfgs, err := ParseConfigs(raw)
	if err != nil {
		t.Fatal(err)
	}
	got := cfgs["gate"]
	if got.Command != "/bin/gate" || got.Args[0] != "-v" || got.Env["TOKEN"] != "s3cret" {
		t.Fatalf("cfg = %+v", got)
	}

	if _, err := ParseConfigs(map[string]json.RawMessage{"x": json.RawMessage(`{}`)}); err == nil {
		t.Fatal("missing command must error")
	}
	if _, err := ParseConfigs(map[string]json.RawMessage{"x": json.RawMessage(`"nope"`)}); err == nil {
		t.Fatal("non-object config must error")
	}
}

func TestDiscoverMergesAndPrefersConfigured(t *testing.T) {
	dir := t.TempDir()
	mustWrite := func(name string, mode os.FileMode) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"), mode); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite("checkpoint", 0o755)    // discovered
	mustWrite("notes.txt", 0o644)     // not executable: skipped
	mustWrite("lint-gate.sh", 0o755)  // extension stripped from the name
	mustWrite("checkpoint2", 0o755)   // discovered
	configured := map[string]Config{"lint-gate": {Command: "/custom/lint-gate"}}

	got := Discover(configured, []string{dir, filepath.Join(dir, "missing")})
	if got["lint-gate"].Command != "/custom/lint-gate" {
		t.Fatalf("configured entry must win: %+v", got["lint-gate"])
	}
	if got["checkpoint"].Command != filepath.Join(dir, "checkpoint") {
		t.Fatalf("checkpoint = %+v", got["checkpoint"])
	}
	if _, ok := got["notes"]; ok {
		t.Fatal("non-executable file must not be discovered")
	}
	if len(got) != 3 {
		t.Fatalf("got %d entries: %+v", len(got), got)
	}
}

func TestOrderedNamesDeterministic(t *testing.T) {
	names := orderedNames(map[string]Config{"b": {}, "a": {}, "c": {}})
	if !reflect.DeepEqual(names, []string{"a", "b", "c"}) {
		t.Fatalf("names = %v", names)
	}
}
