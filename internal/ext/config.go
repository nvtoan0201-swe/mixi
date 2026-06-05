package ext

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// Config is one entry of the settings `extensions` map.
type Config struct {
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"` // values support ${VAR} expansion
}

// ParseConfigs decodes the raw settings map and expands ${VAR} references
// in env values from the process environment (late expansion keeps secrets
// out of the carried settings struct).
func ParseConfigs(raw map[string]json.RawMessage) (map[string]Config, error) {
	out := make(map[string]Config, len(raw))
	for name, body := range raw {
		var cfg Config
		if err := json.Unmarshal(body, &cfg); err != nil {
			return nil, fmt.Errorf("ext: extension %q config: %w", name, err)
		}
		if cfg.Command == "" {
			return nil, fmt.Errorf("ext: extension %q config: missing command", name)
		}
		for k, v := range cfg.Env {
			cfg.Env[k] = os.Expand(v, os.Getenv)
		}
		out[name] = cfg
	}
	return out, nil
}

// DiscoverDirs returns the default auto-discovery directories. The
// project-local dir comes first so it overrides the user's global dir on
// name conflicts (Discover merges first-wins).
func DiscoverDirs(cwd string) []string {
	dirs := []string{filepath.Join(cwd, ".mixi", "extensions")}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, ".mixi", "extensions"))
	}
	return dirs
}

// Discover merges configured extensions with executables found in dirs.
// Configured entries win name conflicts; a discovered file's name is its
// base filename without extension.
func Discover(configured map[string]Config, dirs []string) map[string]Config {
	out := make(map[string]Config, len(configured))
	for name, cfg := range configured {
		out[name] = cfg
	}
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue // missing dir is the normal case
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			info, err := e.Info()
			if err != nil || !isExecutable(info) {
				continue
			}
			name := e.Name()
			if x := filepath.Ext(name); x != "" {
				name = name[:len(name)-len(x)]
			}
			if _, taken := out[name]; taken {
				continue
			}
			out[name] = Config{Command: filepath.Join(dir, e.Name())}
		}
	}
	return out
}

// isExecutable reports whether the file has any execute bit set.
func isExecutable(info fs.FileInfo) bool {
	return info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0
}

// orderedNames returns extension names sorted for deterministic
// registration order — the order blocking gates run in.
func orderedNames(cfgs map[string]Config) []string {
	names := make([]string, 0, len(cfgs))
	for n := range cfgs {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
