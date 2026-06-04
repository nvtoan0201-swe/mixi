// Package config loads mixi settings files and resolves CLI flags into a
// runtime configuration. Precedence: flags > --config file >
// .mixi/settings.json (project) > ~/.mixi/settings.json (global) > defaults.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

// Settings mirrors the settings.json schema. Sections owned by later
// subsystems (permissions, compaction, MCP, extensions) are parsed and
// carried but not yet enforced.
type Settings struct {
	Model       ModelSettings              `json:"model"`
	Permissions PermissionSettings         `json:"permissions"`
	Compaction  CompactionSettings         `json:"compaction"`
	Files       FileSettings               `json:"files"`
	MCPServers  map[string]json.RawMessage `json:"mcpServers"` // decoded by the MCP subsystem
	Extensions  []string                   `json:"extensions"`
}

type ModelSettings struct {
	Default string `json:"default"` // "provider/id"
}

// PermissionSettings is carried for the permission engine. Deny lists only
// ever grow across layers: a project file can add to the global deny set
// but never remove from it.
type PermissionSettings struct {
	Mode         string   `json:"mode"` // plan | prompt | auto-edit | yolo
	Allow        []string `json:"allow"`
	Deny         []string `json:"deny"`
	DenyPatterns []string `json:"denyPatterns"`
}

type CompactionSettings struct {
	Disabled bool `json:"disabled"`
	// ReserveTokens is the context-window headroom that absorbs estimation
	// drift and holds the summary budget (0 → built-in default).
	ReserveTokens int `json:"reserveTokens"`
	// KeepRecentTokens is the conversation tail kept verbatim through a
	// compaction (0 → built-in default).
	KeepRecentTokens int `json:"keepRecentTokens"`
}

type FileSettings struct {
	Fsync bool `json:"fsync"`
}

// LoadSettings merges the settings layers for cwd. extraPath is the
// --config file ("" = none). Missing files are skipped; malformed JSON is
// an error naming the offending file.
func LoadSettings(cwd, extraPath string) (Settings, error) {
	var s Settings
	layers := []string{
		globalSettingsPath(),
		filepath.Join(cwd, ".mixi", "settings.json"),
	}
	if extraPath != "" {
		layers = append(layers, extraPath)
	}
	for i, path := range layers {
		raw, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				// --config names a file explicitly; its absence is a mistake.
				if extraPath != "" && i == len(layers)-1 {
					return s, fmt.Errorf("config: --config file not found: %s", path)
				}
				continue
			}
			return s, fmt.Errorf("config: read %s: %w", path, err)
		}
		if err := mergeLayer(&s, raw); err != nil {
			return s, fmt.Errorf("config: parse %s: %w", path, err)
		}
	}
	expandSettings(&s)
	return s, nil
}

func globalSettingsPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".mixi", "settings.json")
}

// mergeLayer unmarshals one settings file over the accumulated settings.
// JSON object merge gives later layers field-level precedence; deny lists
// are special-cased to append (never weaken) across layers.
func mergeLayer(s *Settings, raw []byte) error {
	// Copy, don't alias: Unmarshal decodes into the existing backing array,
	// so a bare reference would see the new layer's values, not the old.
	prevDeny := append([]string(nil), s.Permissions.Deny...)
	prevPatterns := append([]string(nil), s.Permissions.DenyPatterns...)
	if err := json.Unmarshal(raw, s); err != nil {
		return err
	}
	s.Permissions.Deny = appendUnique(prevDeny, s.Permissions.Deny)
	s.Permissions.DenyPatterns = appendUnique(prevPatterns, s.Permissions.DenyPatterns)
	return nil
}

// appendUnique returns base plus any items of layer not already present.
// When layer is the same slice as base (key absent in the new file,
// unmarshal kept the old value), base is returned untouched.
func appendUnique(base, layer []string) []string {
	seen := make(map[string]struct{}, len(base))
	out := append([]string(nil), base...)
	for _, v := range base {
		seen[v] = struct{}{}
	}
	for _, v := range layer {
		if _, dup := seen[v]; !dup {
			seen[v] = struct{}{}
			out = append(out, v)
		}
	}
	return out
}

// envRef matches ${NAME} only — bare $ stays literal so regex patterns in
// denyPatterns survive expansion.
var envRef = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

func expandEnv(s string) string {
	return envRef.ReplaceAllStringFunc(s, func(m string) string {
		return os.Getenv(m[2 : len(m)-1])
	})
}

// expandSettings applies ${ENV} expansion to every settings string that can
// carry user-supplied values. MCPServers stays raw: its decoder expands at
// parse time so secrets are resolved as late as possible.
func expandSettings(s *Settings) {
	s.Model.Default = expandEnv(s.Model.Default)
	for _, list := range [][]string{
		s.Permissions.Allow, s.Permissions.Deny, s.Permissions.DenyPatterns, s.Extensions,
	} {
		for i, v := range list {
			list[i] = expandEnv(v)
		}
	}
}
