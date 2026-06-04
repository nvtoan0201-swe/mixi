//go:build windows

package session

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"golang.org/x/sys/windows"
)

// Windows is a best-effort, untested platform for mixi: LockFileEx gives
// the same single-writer guarantee flock provides on unix.

type fileLock struct {
	f        *os.File
	hintPath string
}

func lockFile(f *os.File, path string) (*fileLock, error) {
	ol := new(windows.Overlapped)
	err := windows.LockFileEx(windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, ol)
	if err != nil {
		return nil, inUseError(path, err)
	}
	hint := path + ".lock"
	// Best-effort: the OS lock is the real lock; the hint only improves
	// the error message for the loser.
	_ = os.WriteFile(hint, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600)
	return &fileLock{f: f, hintPath: hint}, nil
}

func (l *fileLock) release() {
	_ = os.Remove(l.hintPath)
	ol := new(windows.Overlapped)
	_ = windows.UnlockFileEx(windows.Handle(l.f.Fd()), 0, 1, 0, ol)
}

func inUseError(path string, cause error) error {
	if hint, err := os.ReadFile(path + ".lock"); err == nil {
		if pid := strings.TrimSpace(string(hint)); pid != "" {
			return fmt.Errorf("session: %s is in use by another process (pid %s); use --read-only to inspect it", path, pid)
		}
	}
	return fmt.Errorf("session: %s is in use by another process; use --read-only to inspect it (%w)", path, cause)
}
