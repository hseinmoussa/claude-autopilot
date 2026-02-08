package lock

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ErrLocked is returned when the lockfile is already held by another process.
var ErrLocked = errors.New("lock is held by another process")

// Lock represents an acquired process lock backed by an OS file lock.
type Lock struct {
	fd *os.File
}

// lockInfo is the JSON structure written into the lockfile.
type lockInfo struct {
	PID        int    `json:"pid"`
	AcquiredAt string `json:"acquired_at"`
}

// AcquireLock opens the lockfile at path and attempts to acquire an exclusive
// non-blocking lock. On success, the file is truncated and populated with the
// current PID and timestamp before returning the Lock handle.
//
// If the lock is already held, the function reads the existing
// lockfile to discover the holder PID. If the file is empty (race with another
// writer), it retries the read once after 500ms. Returns ErrLocked wrapped
// with the holder PID information.
func AcquireLock(path string) (*Lock, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create lock directory %s: %w", dir, err)
	}

	fd, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return nil, fmt.Errorf("open lockfile %s: %w", path, err)
	}

	err = tryExclusiveLock(fd)
	if err != nil {
		// Lock is held by another process.
		if isLockHeldError(err) {
			holderPID := readHolderPID(fd)
			fd.Close()
			return nil, fmt.Errorf("%w: held by PID %d", ErrLocked, holderPID)
		}
		fd.Close()
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}

	// Lock acquired. Truncate and write our metadata.
	if err := fd.Truncate(0); err != nil {
		fd.Close()
		return nil, fmt.Errorf("truncate lockfile: %w", err)
	}
	if _, err := fd.Seek(0, 0); err != nil {
		fd.Close()
		return nil, fmt.Errorf("seek lockfile: %w", err)
	}

	info := lockInfo{
		PID:        os.Getpid(),
		AcquiredAt: time.Now().UTC().Format(time.RFC3339),
	}
	data, _ := json.Marshal(info)
	if _, err := fd.Write(data); err != nil {
		fd.Close()
		return nil, fmt.Errorf("write lockfile metadata: %w", err)
	}
	if err := fd.Sync(); err != nil {
		fd.Close()
		return nil, fmt.Errorf("fsync lockfile: %w", err)
	}

	return &Lock{fd: fd}, nil
}

// TryLock attempts to acquire the lock in a non-blocking manner.
// Returns (lock, true, nil) if acquired, (nil, false, nil) if held by another
// process, or (nil, false, err) on unexpected errors.
func TryLock(path string) (*Lock, bool, error) {
	l, err := AcquireLock(path)
	if err != nil {
		if errors.Is(err, ErrLocked) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return l, true, nil
}

// Release closes the file descriptor, which releases the flock.
func (l *Lock) Release() error {
	if l.fd == nil {
		return nil
	}
	err := l.fd.Close()
	l.fd = nil
	return err
}

// ReadLockInfo reads the PID and acquired_at timestamp from an existing
// lockfile without attempting to acquire the lock.
func ReadLockInfo(path string) (pid int, acquiredAt time.Time, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, time.Time{}, fmt.Errorf("read lockfile %s: %w", path, err)
	}

	if len(data) == 0 {
		// Possibly being written; wait briefly and retry.
		time.Sleep(500 * time.Millisecond)
		data, err = os.ReadFile(path)
		if err != nil {
			return 0, time.Time{}, fmt.Errorf("read lockfile %s (retry): %w", path, err)
		}
	}

	if len(data) == 0 {
		return 0, time.Time{}, fmt.Errorf("lockfile %s is empty", path)
	}

	var info lockInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return 0, time.Time{}, fmt.Errorf("parse lockfile %s: %w", path, err)
	}

	t, err := time.Parse(time.RFC3339, info.AcquiredAt)
	if err != nil {
		return info.PID, time.Time{}, fmt.Errorf("parse acquired_at in %s: %w", path, err)
	}

	return info.PID, t, nil
}

// readHolderPID reads the PID from the lockfile fd. If the file is empty
// (writer hasn't flushed yet), it retries once after 500ms.
func readHolderPID(fd *os.File) int {
	if _, err := fd.Seek(0, 0); err != nil {
		return 0
	}

	data := make([]byte, 256)
	n, _ := fd.Read(data)
	if n == 0 {
		// Retry after a brief delay - writer may not have flushed yet.
		time.Sleep(500 * time.Millisecond)
		if _, err := fd.Seek(0, 0); err != nil {
			return 0
		}
		n, _ = fd.Read(data)
	}

	if n == 0 {
		return 0
	}

	var info lockInfo
	if err := json.Unmarshal(data[:n], &info); err != nil {
		return 0
	}
	return info.PID
}
