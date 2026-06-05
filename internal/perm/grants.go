package perm

import (
	"path/filepath"
	"strings"
	"sync"
)

// grantStore holds the session's "always allow" grants. In-memory only:
// grants die with the session and are never written to settings, so a
// drive-by approval can't become a permanent privilege.
type grantStore struct {
	mu    sync.Mutex
	rules []Rule
}

func (g *grantStore) add(r Rule) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, have := range g.rules {
		if have.raw == r.raw {
			return
		}
	}
	g.rules = append(g.rules, r)
}

func (g *grantStore) match(c CallInfo) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return firstMatch(g.rules, c) != nil
}

func (g *grantStore) list() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]string, len(g.rules))
	for i, r := range g.rules {
		out[i] = r.raw
	}
	return out
}

// generalize widens one approved call into a session grant: bash keeps its
// first two tokens (`go test ./...` → `bash(go test*)`) so sibling
// invocations pass without re-asking; file ops grant the containing
// directory; anything else gets an exact-match grant.
func generalize(c CallInfo) Rule {
	switch {
	case c.Tool == "bash":
		fields := strings.Fields(c.Command)
		prefix := c.Command
		if len(fields) > 2 {
			prefix = fields[0] + " " + fields[1]
		} else if len(fields) > 0 {
			prefix = strings.Join(fields, " ")
		}
		raw := "bash(" + prefix + "*)"
		return Rule{Tool: "bash", Spec: prefix + "*", raw: raw}
	case c.Path != "":
		spec := filepath.Dir(c.Path) + "/**"
		return Rule{Tool: c.Tool, Spec: spec, raw: c.Tool + "(" + spec + ")"}
	case c.Category == CatMCP:
		return Rule{Spec: c.Tool, raw: c.Tool}
	default:
		return Rule{Tool: c.Tool, raw: c.Tool}
	}
}
