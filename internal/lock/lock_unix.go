//go:build !windows

package lock

import (
	"errors"
	"os"
	"syscall"
)

func tryExclusiveLock(fd *os.File) error {
	return syscall.Flock(int(fd.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}

func isLockHeldError(err error) bool {
	return errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN)
}
