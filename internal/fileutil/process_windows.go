//go:build windows

package fileutil

import "golang.org/x/sys/windows"

// isProcessAlive checks if a process with the given PID is alive.
// Uses OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION); invalid PID returns false.
func isProcessAlive(pid int) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		if err == windows.ERROR_INVALID_PARAMETER {
			return false
		}
		// On permission errors, assume alive to avoid false deletes.
		return true
	}
	_ = windows.CloseHandle(h)
	return true
}
