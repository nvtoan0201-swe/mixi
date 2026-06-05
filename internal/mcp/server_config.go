package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"time"
)

// ServerConfig is one entry of the settings `mcpServers` map.
type ServerConfig struct {
	Command   string            `json:"command"`
	Args      []string          `json:"args"`
	Env       map[string]string `json:"env"` // values support ${VAR} expansion
	TimeoutMs int               `json:"timeoutMs"`
}

// Timeout converts the configured per-call budget, defaulting when unset.
// It also bounds the handshake, so startup waits derive from it.
func (c ServerConfig) Timeout() time.Duration {
	if c.TimeoutMs <= 0 {
		return DefaultCallTimeout
	}
	return time.Duration(c.TimeoutMs) * time.Millisecond
}

// ParseServers decodes the raw settings map and expands ${VAR} references
// in env values from the process environment.
func ParseServers(raw map[string]json.RawMessage) (map[string]ServerConfig, error) {
	out := make(map[string]ServerConfig, len(raw))
	for name, body := range raw {
		var cfg ServerConfig
		if err := json.Unmarshal(body, &cfg); err != nil {
			return nil, fmt.Errorf("mcp: server %q config: %w", name, err)
		}
		if cfg.Command == "" {
			return nil, fmt.Errorf("mcp: server %q config: missing command", name)
		}
		for k, v := range cfg.Env {
			cfg.Env[k] = os.Expand(v, os.Getenv)
		}
		out[name] = cfg
	}
	return out, nil
}

// toolNameChars matches everything outside the Anthropic-safe tool-name
// charset; offending runs collapse to a single underscore.
var toolNameChars = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)

// sanitizeName maps an arbitrary server key or tool name into the safe
// charset.
func sanitizeName(s string) string {
	return toolNameChars.ReplaceAllString(s, "_")
}

// RegisteredToolName builds the namespaced registry name for a server tool.
func RegisteredToolName(server, tool string) string {
	return "mcp__" + sanitizeName(server) + "__" + sanitizeName(tool)
}
