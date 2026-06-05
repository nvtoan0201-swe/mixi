//go:build !unix

package procgroup

import (
	"os/exec"
	"syscall"
)

// Windows is best-effort and untested (locked project decision): no process
// groups, so only the direct child is killed — grandchildren may survive.

func Set(cmd *exec.Cmd) {}

func Kill(cmd *exec.Cmd, _ syscall.Signal) {
	if cmd.Process != nil {
		cmd.Process.Kill()
	}
}
