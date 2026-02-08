//go:build windows

package lock

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

func tryExclusiveLock(fd *os.File) error {
	h := windows.Handle(fd.Fd())
	var ov windows.Overlapped
	return windows.LockFileEx(h, windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &ov)
}

func isLockHeldError(err error) bool {
	return errors.Is(err, windows.ERROR_LOCK_VIOLATION)
}
