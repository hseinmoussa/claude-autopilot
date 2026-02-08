//go:build !windows

package queue

import (
	"os"
	"syscall"
)

func lockFileExclusive(fd *os.File) error {
	return syscall.Flock(int(fd.Fd()), syscall.LOCK_EX)
}

func unlockFile(fd *os.File) error {
	return syscall.Flock(int(fd.Fd()), syscall.LOCK_UN)
}
