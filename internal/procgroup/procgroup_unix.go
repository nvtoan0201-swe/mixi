//go:build unix

// Package procgroup isolates subprocess trees so kill escalation reaches
// every descendant. Shared by the subprocess protocols (MCP servers,
// extension host).
package procgroup

import (
	"os/exec"
	"syscall"
)

// Set puts the child in its own process group so signals can target the
// whole process tree, not just the immediate child.
func Set(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// Kill signals the whole process group (Set makes the child's pgid equal
// its pid); falls back to the single process if that fails.
func Kill(cmd *exec.Cmd, sig syscall.Signal) {
	if cmd.Process == nil {
		return
	}
	if err := syscall.Kill(-cmd.Process.Pid, sig); err != nil {
		cmd.Process.Signal(sig)
	}
}
