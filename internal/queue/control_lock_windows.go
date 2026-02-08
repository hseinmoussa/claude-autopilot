//go:build windows

package queue

import (
	"os"

	"golang.org/x/sys/windows"
)

func lockFileExclusive(fd *os.File) error {
	h := windows.Handle(fd.Fd())
	var ov windows.Overlapped
	return windows.LockFileEx(h, windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, &ov)
}

func unlockFile(fd *os.File) error {
	h := windows.Handle(fd.Fd())
	var ov windows.Overlapped
	return windows.UnlockFileEx(h, 0, 1, 0, &ov)
}
