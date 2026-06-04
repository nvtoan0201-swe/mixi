//go:build unix

package session

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// fileLock holds an advisory flock on the session file plus a best-effort
// sidecar pid-hint file so the "in use" error can name the other process.
type fileLock struct {
	f        *os.File // the locked session file (not owned; caller closes)
	hintPath string
}

// lockFile takes flock(LOCK_EX|LOCK_NB) on the already-open session file.
// Contention returns a clear in-use error with the pid hint when one was
// left behind by the lock holder.
func lockFile(f *os.File, path string) (*fileLock, error) {
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return nil, inUseError(path, err)
	}
	hint := path + ".lock"
	// Best-effort: the flock is the real lock; the hint only improves the
	// error message for the loser.
	_ = os.WriteFile(hint, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600)
	return &fileLock{f: f, hintPath: hint}, nil
}

func (l *fileLock) release() {
	_ = os.Remove(l.hintPath)
	_ = syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
}

func inUseError(path string, cause error) error {
	if hint, err := os.ReadFile(path + ".lock"); err == nil {
		if pid := strings.TrimSpace(string(hint)); pid != "" {
			return fmt.Errorf("session: %s is in use by another process (pid %s); use --read-only to inspect it", path, pid)
		}
	}
	return fmt.Errorf("session: %s is in use by another process; use --read-only to inspect it (%w)", path, cause)
}
