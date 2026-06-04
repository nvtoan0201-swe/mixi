package config

import (
	"io"
	"strings"
	"testing"
)

func parse(t *testing.T, args ...string) *Flags {
	t.Helper()
	f, err := ParseFlags(args, io.Discard)
	if err != nil {
		t.Fatalf("ParseFlags(%v): %v", args, err)
	}
	return f
}

func TestRepeatableAndAliasFlags(t *testing.T) {
	f := parse(t, "-p", "first", "--message", "two", "--message", "three", "--continue")
	if f.Print != "first" || len(f.Messages) != 2 || f.Messages[1] != "three" {
		t.Fatalf("parsed = %+v", f)
	}
	if !f.Continue || !f.WasSet("c") {
		t.Fatal("--continue alias should set the canonical c flag")
	}
	if !f.WasSet("p") || f.WasSet("model") {
		t.Fatal("WasSet must track explicit flags only")
	}
}

func TestBadFlagAndEnumValuesError(t *testing.T) {
	cases := [][]string{
		{"--no-such-flag"},
		{"--output", "yaml"},
		{"--thinking", "max"},
		{"--log-level", "loud"},
		{"--permission-mode", "chaos"},
		{"--max-turns", "-1"},
		{"--fork", "abc123"}, // fork without resume
		{"stray-positional"},
	}
	for _, args := range cases {
		if _, err := ParseFlags(args, io.Discard); err == nil {
			t.Errorf("ParseFlags(%v): want error", args)
		}
	}
}

func TestReplaySubcommand(t *testing.T) {
	f := parse(t, "replay", "/tmp/session.jsonl")
	if !f.IsReplay || f.ReplayFile != "/tmp/session.jsonl" {
		t.Fatalf("replay parse = %+v", f)
	}
}

func TestResolveModeSelection(t *testing.T) {
	cases := []struct {
		args  []string
		stdin string
		want  Mode
	}{
		{[]string{"-p", "hi"}, "", ModePrint},
		{[]string{}, "piped data", ModePrint},
		{[]string{"--rpc"}, "", ModeRPC},
		{[]string{"replay", "f.jsonl"}, "", ModeReplay},
		{[]string{}, "", ModeTUI},
	}
	for _, c := range cases {
		f := parse(t, c.args...)
		rc, err := Resolve(f, Settings{}, "/tmp", c.stdin)
		if err != nil {
			t.Fatal(err)
		}
		if rc.Mode != c.want {
			t.Errorf("Resolve(%v, stdin=%q).Mode = %s, want %s", c.args, c.stdin, rc.Mode, c.want)
		}
	}
}

func TestResolvePromptJoinsFlagAndStdin(t *testing.T) {
	f := parse(t, "-p", "explain this")
	rc, err := Resolve(f, Settings{}, "/tmp", "piped file contents")
	if err != nil {
		t.Fatal(err)
	}
	if rc.Prompt != "explain this\n\npiped file contents" {
		t.Fatalf("Prompt = %q", rc.Prompt)
	}
}

func TestModelPrecedenceFlagOverSettingsOverDefault(t *testing.T) {
	f := parse(t, "-p", "x")
	rc, err := Resolve(f, Settings{}, "/tmp", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := rc.Model.Provider + "/" + rc.Model.ID; got != DefaultModel {
		t.Errorf("default model = %q, want %q", got, DefaultModel)
	}

	s := Settings{Model: ModelSettings{Default: "anthropic/claude-haiku-4-5"}}
	rc, err = Resolve(f, s, "/tmp", "")
	if err != nil {
		t.Fatal(err)
	}
	if rc.Model.ID != "claude-haiku-4-5" {
		t.Errorf("settings model = %q", rc.Model.ID)
	}

	f = parse(t, "-p", "x", "--model", "faux/scripted")
	rc, err = Resolve(f, s, "/tmp", "")
	if err != nil {
		t.Fatal(err)
	}
	if rc.Model.Provider != "faux" {
		t.Errorf("flag model = %+v, want faux", rc.Model)
	}
}

func TestResolveModelErrors(t *testing.T) {
	for _, spec := range []string{"nope", "anthropic/", "/x", "anthropic/unknown-model"} {
		if _, err := ResolveModel(spec); err == nil {
			t.Errorf("ResolveModel(%q): want error", spec)
		}
	}
	if _, err := ResolveModel("anthropic/claude-opus-4-8"); err != nil {
		t.Errorf("known model: %v", err)
	}
}

func TestUsagePrintedOnError(t *testing.T) {
	var sb strings.Builder
	if _, err := ParseFlags([]string{"--no-such-flag"}, &sb); err == nil {
		t.Fatal("want error")
	}
	if !strings.Contains(sb.String(), "Usage") && !strings.Contains(sb.String(), "-p") {
		t.Errorf("usage not printed: %q", sb.String())
	}
}
