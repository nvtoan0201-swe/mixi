package ext

import (
	"errors"
	"regexp"

	"github.com/user/mixi-agent/internal/wire"
)

// toolNameOK matches the Anthropic-safe tool-name charset; extension tools
// register under their raw names (no namespacing — conflicts are resolved,
// not avoided: built-ins win, first-registered extension wins).
var toolNameOK = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,128}$`)

// isLineTooLong reports a wire line-cap violation.
func isLineTooLong(err error) bool { return errors.Is(err, wire.ErrLineTooLong) }

// adopt applies a validated hello: subscriptions, tools, commands. The
// first hello fixes the extension's catalog; a re-hello after restart must
// not change the LLM-visible tool list, so later additions are rejected
// with a WARN and removals only logged.
func (h *Host) adopt(e *extension, hello *Hello) error {
	subs := map[string]bool{}
	for _, s := range hello.Subscribe {
		if s == "*" || knownEvents[s] {
			subs[s] = true
			continue
		}
		h.log.Warn("ext: unknown event in subscribe, ignoring", "ext", e.name, "event", s)
	}

	e.mu.Lock()
	firstHello := e.tools == nil && e.commands == nil
	e.subs = subs
	e.mu.Unlock()

	if !firstHello {
		h.logCatalogDrift(e, hello)
		return nil
	}

	adopted := make([]ToolDef, 0, len(hello.Tools))
	for _, td := range hello.Tools {
		if !toolNameOK.MatchString(td.Name) {
			h.log.Warn("ext: rejecting tool with invalid name", "ext", e.name, "tool", td.Name)
			continue
		}
		if h.reg == nil {
			continue
		}
		if err := h.reg.Register(&extTool{host: h, ext: e, def: td}); err != nil {
			// Built-ins win; among extensions, first registered wins.
			h.log.Warn("ext: tool name conflict, rejecting", "ext", e.name, "tool", td.Name, "err", err)
			continue
		}
		adopted = append(adopted, td)
	}

	commands := make([]CommandDef, 0, len(hello.Commands))
	for _, c := range hello.Commands {
		if !toolNameOK.MatchString(c.Name) {
			h.log.Warn("ext: rejecting command with invalid name", "ext", e.name, "command", c.Name)
			continue
		}
		commands = append(commands, c)
	}

	e.mu.Lock()
	e.tools = adopted
	e.commands = commands
	e.mu.Unlock()
	return nil
}

// logCatalogDrift surfaces tool-set changes a restarted extension tried to
// make; the registered catalog stays as the first hello declared it.
func (h *Host) logCatalogDrift(e *extension, hello *Hello) {
	e.mu.Lock()
	known := make(map[string]bool, len(e.tools))
	for _, td := range e.tools {
		known[td.Name] = true
	}
	e.mu.Unlock()
	for _, td := range hello.Tools {
		if !known[td.Name] {
			h.log.Warn("ext: restarted extension declared a new tool, ignored until next session",
				"ext", e.name, "tool", td.Name)
		}
	}
}

// offersTool reports whether the extension's adopted catalog has the tool.
func (e *extension) offersTool(name string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, td := range e.tools {
		if td.Name == name {
			return true
		}
	}
	return false
}
