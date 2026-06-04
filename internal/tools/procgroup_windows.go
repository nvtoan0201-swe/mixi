//go:build !unix

package tools

import "os/exec"

// Windows is best-effort and untested (locked project decision): no process
// groups, so only the direct child is killed — grandchildren may survive.

func setProcGroup(cmd *exec.Cmd) {}

func terminateGroup(cmd *exec.Cmd, exited <-chan struct{}) {
	if cmd.Process != nil {
		cmd.Process.Kill()
	}
}
