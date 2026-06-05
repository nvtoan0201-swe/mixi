//go:build !unix

package mcp

import (
	"os/exec"
	"syscall"
)

// Windows is best-effort and untested (locked project decision): no process
// groups, so only the direct child is killed — grandchildren may survive.

func setProcGroup(cmd *exec.Cmd) {}

func groupKill(cmd *exec.Cmd, _ syscall.Signal) {
	if cmd.Process != nil {
		cmd.Process.Kill()
	}
}
