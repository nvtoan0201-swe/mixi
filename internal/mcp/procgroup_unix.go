//go:build unix

package mcp

import (
	"os/exec"
	"syscall"
)

// setProcGroup puts the server in its own process group so kill escalation
// reaches the whole process tree, not just the immediate child.
func setProcGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// groupKill signals the whole process group (Setpgid makes the child's
// pgid equal its pid); falls back to the single process if that fails.
func groupKill(cmd *exec.Cmd, sig syscall.Signal) {
	if cmd.Process == nil {
		return
	}
	if err := syscall.Kill(-cmd.Process.Pid, sig); err != nil {
		cmd.Process.Signal(sig)
	}
}
