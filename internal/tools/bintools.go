package tools

import (
	"os/exec"
	"runtime"
)

// lookPath is swappable in tests to simulate missing/present binaries.
var lookPath = exec.LookPath

// findBinary returns the first of names found on PATH (fd ships as fdfind
// on Debian/Ubuntu).
func findBinary(names ...string) (string, bool) {
	for _, n := range names {
		if p, err := lookPath(n); err == nil {
			return p, true
		}
	}
	return "", false
}

// missingRipgrepError tells the model how to get rg installed — the project
// never auto-downloads binaries (locked decision).
func missingRipgrepError() ToolResult {
	var hint string
	switch runtime.GOOS {
	case "darwin":
		hint = "brew install ripgrep"
	case "windows":
		hint = "winget install BurntSushi.ripgrep.MSVC (or choco install ripgrep)"
	default:
		hint = "apt install ripgrep (or your distro's package manager)"
	}
	return Errorf("grep: ripgrep (rg) not found on PATH. Install it: %s", hint)
}

// commandFailedError surfaces a non-zero subprocess exit with its stderr.
func commandFailedError(name string, err error, stderr string) ToolResult {
	if stderr != "" {
		return Errorf("%s failed: %v\n%s", name, err, clipLine(stderr, GrepMaxLineLen))
	}
	return Errorf("%s failed: %v", name, err)
}
