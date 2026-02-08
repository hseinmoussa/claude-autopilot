//go:build !windows

package fileutil

import "syscall"

// isProcessAlive checks if a process with the given PID is alive.
// Uses kill(pid, 0) on POSIX (returns ESRCH if dead).
func isProcessAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}
