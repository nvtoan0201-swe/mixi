package perm

import (
	"path/filepath"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// wildcardMatch is a flat glob over an arbitrary string: `*` matches any run
// of characters including spaces and slashes (command strings are not paths).
func wildcardMatch(pattern, s string) bool {
	parts := strings.Split(pattern, "*")
	if len(parts) == 1 {
		return pattern == s
	}
	if !strings.HasPrefix(s, parts[0]) {
		return false
	}
	s = s[len(parts[0]):]
	for _, part := range parts[1 : len(parts)-1] {
		i := strings.Index(s, part)
		if i < 0 {
			return false
		}
		s = s[i+len(part):]
	}
	return strings.HasSuffix(s, parts[len(parts)-1])
}

// pathGlobMatch applies a doublestar glob to the cleaned absolute path.
// Patterns starting with "/" match absolutely; "**" patterns float (match at
// any depth); anything else is anchored at cwd.
func pathGlobMatch(pattern, absPath, cwd string) bool {
	if absPath == "" {
		return false
	}
	switch {
	case strings.HasPrefix(pattern, "/"):
		// absolute, use as-is
	case strings.HasPrefix(pattern, "**"):
		// floating: strip the leading separator so `**/...` sees relative
		// segments and can also match at the root of the tree
		ok, _ := doublestar.Match(pattern, strings.TrimPrefix(absPath, "/"))
		return ok
	default:
		pattern = filepath.Join(cwd, pattern)
	}
	ok, _ := doublestar.Match(pattern, absPath)
	return ok
}
