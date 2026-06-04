//go:build unix

package tools

import (
	"os/exec"
	"syscall"
	"time"
)

// setProcGroup puts the shell in its own process group so timeouts and
// kills reach the entire process tree, not just the immediate child.
func setProcGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// groupSignal signals the whole process group (Setpgid makes the child's
// pgid equal its pid); falls back to the single process if that fails.
func groupSignal(cmd *exec.Cmd, sig syscall.Signal) {
	if cmd.Process == nil {
		return
	}
	if err := syscall.Kill(-cmd.Process.Pid, sig); err != nil {
		cmd.Process.Signal(sig)
	}
}

// terminateGroup asks the group to exit (SIGTERM), then SIGKILLs it after a
// 2s grace unless exited closes first.
func terminateGroup(cmd *exec.Cmd, exited <-chan struct{}) {
	groupSignal(cmd, syscall.SIGTERM)
	go func() {
		select {
		case <-exited:
		case <-time.After(2 * time.Second):
			groupSignal(cmd, syscall.SIGKILL)
		}
	}()
}
