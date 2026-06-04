package tools

import (
	"os"
	"path/filepath"
)

// Options configures the built-in tool set.
type Options struct {
	// Cwd anchors relative tool paths; empty means the process working dir.
	Cwd string
	// Fsync forces fsync after file writes (config files.fsync, default off).
	Fsync bool
}

// RegisterBuiltins adds the nine built-in tools to r and returns the shared
// background-job table so the agent can kill leftover jobs on shutdown.
func RegisterBuiltins(r *Registry, opts Options) (*JobTable, error) {
	cwd := opts.Cwd
	if cwd == "" {
		if wd, err := os.Getwd(); err == nil {
			cwd = wd
		}
	}
	mq := newMutQueue()
	jobs := NewJobTable()
	all := []Tool{
		&readTool{cwd: cwd},
		&writeTool{cwd: cwd, fsync: opts.Fsync, mq: mq},
		&editTool{cwd: cwd, mq: mq},
		&bashTool{cwd: cwd, jobs: jobs},
		&bashOutputTool{jobs: jobs},
		&killBashTool{jobs: jobs},
		&grepTool{cwd: cwd},
		&findTool{cwd: cwd},
		&lsTool{cwd: cwd},
	}
	for _, t := range all {
		if err := r.Register(t); err != nil {
			return nil, err
		}
	}
	return jobs, nil
}

// resolvePath anchors path at cwd when relative and cleans it.
func resolvePath(cwd, path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(cwd, path)
}
