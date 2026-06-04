package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeSettings lays out a fake home + project dir with settings files.
func writeSettings(t *testing.T, dir, rel, content string) string {
	t.Helper()
	path := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSettingsLayerPrecedence(t *testing.T) {
	home, proj := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	writeSettings(t, home, ".mixi/settings.json",
		`{"model":{"default":"anthropic/claude-haiku-4-5"},"files":{"fsync":true}}`)
	writeSettings(t, proj, ".mixi/settings.json",
		`{"model":{"default":"anthropic/claude-sonnet-4-6"}}`)

	s, err := LoadSettings(proj, "")
	if err != nil {
		t.Fatal(err)
	}
	if s.Model.Default != "anthropic/claude-sonnet-4-6" {
		t.Errorf("project should override global model: got %q", s.Model.Default)
	}
	if !s.Files.Fsync {
		t.Error("absent project key should keep global fsync=true")
	}

	extra := writeSettings(t, t.TempDir(), "extra.json",
		`{"model":{"default":"anthropic/claude-opus-4-8"}}`)
	s, err = LoadSettings(proj, extra)
	if err != nil {
		t.Fatal(err)
	}
	if s.Model.Default != "anthropic/claude-opus-4-8" {
		t.Errorf("--config should win over project: got %q", s.Model.Default)
	}
}

func TestDenyListsOnlyGrowAcrossLayers(t *testing.T) {
	home, proj := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	writeSettings(t, home, ".mixi/settings.json",
		`{"permissions":{"mode":"prompt","allow":["bash(go test*)"],"deny":["read(**/.env*)"]}}`)
	writeSettings(t, proj, ".mixi/settings.json",
		`{"permissions":{"mode":"yolo","allow":["bash(make*)"],"deny":["write(/etc/**)"]}}`)

	s, err := LoadSettings(proj, "")
	if err != nil {
		t.Fatal(err)
	}
	if s.Permissions.Mode != "yolo" {
		t.Errorf("mode should override: got %q", s.Permissions.Mode)
	}
	if len(s.Permissions.Allow) != 1 || s.Permissions.Allow[0] != "bash(make*)" {
		t.Errorf("allow should override: got %v", s.Permissions.Allow)
	}
	want := []string{"read(**/.env*)", "write(/etc/**)"}
	if len(s.Permissions.Deny) != 2 || s.Permissions.Deny[0] != want[0] || s.Permissions.Deny[1] != want[1] {
		t.Errorf("deny should append, never weaken: got %v", s.Permissions.Deny)
	}
}

func TestEnvExpansionBracesOnly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("MIXI_TEST_MODEL", "anthropic/claude-haiku-4-5")
	writeSettings(t, home, ".mixi/settings.json",
		`{"model":{"default":"${MIXI_TEST_MODEL}"},"permissions":{"denyPatterns":["rm\\s+/$"]}}`)

	s, err := LoadSettings(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	if s.Model.Default != "anthropic/claude-haiku-4-5" {
		t.Errorf("${ENV} not expanded: got %q", s.Model.Default)
	}
	if s.Permissions.DenyPatterns[0] != `rm\s+/$` {
		t.Errorf("bare $ must stay literal in regex patterns: got %q", s.Permissions.DenyPatterns[0])
	}
}

func TestMissingConfigFlagFileErrors(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if _, err := LoadSettings(t.TempDir(), "/nonexistent/extra.json"); err == nil {
		t.Fatal("absent --config file should error, absent default layers should not")
	}
}

func TestMalformedSettingsNamesFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeSettings(t, home, ".mixi/settings.json", "{broken")
	_, err := LoadSettings(t.TempDir(), "")
	if err == nil || !strings.Contains(err.Error(), "settings.json") {
		t.Fatalf("err = %v, want parse error naming the file", err)
	}
}
