package fileutil

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// TempFileName generates a temp file name: <filename>.tmp.<pid>.<random>
func TempFileName(path string) string {
	pid := os.Getpid()
	b := make([]byte, 4)
	rand.Read(b)
	return fmt.Sprintf("%s.tmp.%d.%s", path, pid, hex.EncodeToString(b))
}

// AtomicWrite writes data to path using temp+rename for crash safety.
// Used for mutable files like .state.json.
func AtomicWrite(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create directory %s: %w", dir, err)
	}

	tmp := TempFileName(path)
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}

	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("write temp file: %w", err)
	}

	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("fsync temp file: %w", err)
	}

	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("close temp file: %w", err)
	}

	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename temp to target: %w", err)
	}

	// fsync parent dir on POSIX for directory entry durability
	if runtime.GOOS != "windows" {
		fsyncDir(dir)
	}

	return nil
}

// AtomicCreate creates a file using temp+hardlink for race-safe create-once semantics.
// Used for .init.json files. Returns (true, nil) if created, (false, nil) if already exists.
func AtomicCreate(path string, data []byte, perm os.FileMode) (bool, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return false, fmt.Errorf("create directory %s: %w", dir, err)
	}

	tmp := TempFileName(path)
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return false, fmt.Errorf("create temp file: %w", err)
	}

	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return false, fmt.Errorf("write temp file: %w", err)
	}

	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return false, fmt.Errorf("fsync temp file: %w", err)
	}

	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return false, fmt.Errorf("close temp file: %w", err)
	}

	// hardlink: fails with EEXIST if target already exists
	err = os.Link(tmp, path)
	os.Remove(tmp) // always clean up temp

	if err != nil {
		if os.IsExist(err) {
			return false, nil // another process won the race
		}
		// Check for unsupported filesystem
		if isLinkUnsupported(err) {
			return false, fmt.Errorf("init file creation failed: filesystem does not support hardlinks. Move ~/.claude-autopilot/state to a local filesystem (ext4, APFS, NTFS)")
		}
		return false, fmt.Errorf("hardlink temp to target: %w", err)
	}

	// fsync parent dir
	if runtime.GOOS != "windows" {
		fsyncDir(dir)
	}

	return true, nil
}

func fsyncDir(dir string) {
	d, err := os.Open(dir)
	if err != nil {
		return
	}
	d.Sync()
	d.Close()
}

func isLinkUnsupported(err error) bool {
	s := err.Error()
	return strings.Contains(s, "not supported") ||
		strings.Contains(s, "not permitted") ||
		strings.Contains(s, "operation not supported")
}

// CleanOrphanTemps sweeps temp files in the given directories.
// Pass 1: delete if owner PID is dead. Pass 2: delete if mtime > 24h.
func CleanOrphanTemps(dirs []string) (int, error) {
	cleaned := 0
	now := time.Now()

	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return cleaned, err
		}

		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := entry.Name()
			if !strings.Contains(name, ".tmp.") {
				continue
			}

			fullPath := filepath.Join(dir, name)

			// Extract PID from filename: <base>.tmp.<pid>.<random>
			pid := extractPID(name)

			// Pass 1: dead-owner cleanup
			if pid > 0 && !isProcessAlive(pid) {
				os.Remove(fullPath)
				cleaned++
				continue
			}

			// Pass 2: age-based fallback (mtime > 24h)
			info, err := entry.Info()
			if err != nil {
				continue
			}
			if now.Sub(info.ModTime()) > 24*time.Hour {
				os.Remove(fullPath)
				cleaned++
			}
		}
	}

	return cleaned, nil
}

func extractPID(name string) int {
	// Format: <base>.tmp.<pid>.<random>
	idx := strings.Index(name, ".tmp.")
	if idx < 0 {
		return 0
	}
	rest := name[idx+5:] // after ".tmp."
	parts := strings.SplitN(rest, ".", 2)
	if len(parts) < 1 {
		return 0
	}
	pid, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0
	}
	return pid
}
