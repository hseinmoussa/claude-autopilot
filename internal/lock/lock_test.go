package lock

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// AcquireLock
// ---------------------------------------------------------------------------

func TestAcquireLock_SucceedsOnFreshFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.lock")

	l, err := AcquireLock(path)
	if err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}
	defer l.Release()

	if l == nil {
		t.Fatal("expected non-nil lock")
	}
}

func TestAcquireLock_SecondAttemptReturnsErrLocked(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.lock")

	l1, err := AcquireLock(path)
	if err != nil {
		t.Fatalf("first AcquireLock: %v", err)
	}
	defer l1.Release()

	_, err = AcquireLock(path)
	if err == nil {
		t.Fatal("second AcquireLock should fail")
	}
	if !errors.Is(err, ErrLocked) {
		t.Errorf("error = %v; want ErrLocked", err)
	}
}

func TestAcquireLock_CreatesDirs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "deep", "test.lock")

	l, err := AcquireLock(path)
	if err != nil {
		t.Fatalf("AcquireLock should create dirs: %v", err)
	}
	defer l.Release()
}

// ---------------------------------------------------------------------------
// TryLock
// ---------------------------------------------------------------------------

func TestTryLock_SuccessOnFresh(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.lock")

	l, acquired, err := TryLock(path)
	if err != nil {
		t.Fatalf("TryLock: %v", err)
	}
	if !acquired {
		t.Error("TryLock should return true on fresh file")
	}
	if l == nil {
		t.Error("TryLock should return non-nil lock on success")
	}
	defer l.Release()
}

func TestTryLock_ContentionReturnsFalse(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.lock")

	l1, _, err := TryLock(path)
	if err != nil {
		t.Fatalf("first TryLock: %v", err)
	}
	defer l1.Release()

	l2, acquired, err := TryLock(path)
	if err != nil {
		t.Fatalf("second TryLock: %v", err)
	}
	if acquired {
		t.Error("second TryLock should return false (contention)")
	}
	if l2 != nil {
		t.Error("second TryLock should return nil lock on contention")
	}
}

// ---------------------------------------------------------------------------
// Release
// ---------------------------------------------------------------------------

func TestRelease_AllowsReacquisition(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.lock")

	l1, err := AcquireLock(path)
	if err != nil {
		t.Fatalf("first AcquireLock: %v", err)
	}

	if err := l1.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}

	// Should be able to re-acquire after release.
	l2, err := AcquireLock(path)
	if err != nil {
		t.Fatalf("re-AcquireLock after release: %v", err)
	}
	defer l2.Release()
}

func TestRelease_DoubleReleaseIsSafe(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.lock")

	l, err := AcquireLock(path)
	if err != nil {
		t.Fatal(err)
	}

	if err := l.Release(); err != nil {
		t.Fatalf("first Release: %v", err)
	}
	if err := l.Release(); err != nil {
		t.Fatalf("second Release should be safe: %v", err)
	}
}

// ---------------------------------------------------------------------------
// ReadLockInfo
// ---------------------------------------------------------------------------

func TestReadLockInfo_ReturnsPIDAndTimestamp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.lock")

	before := time.Now().UTC().Add(-1 * time.Second)

	l, err := AcquireLock(path)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Release()

	after := time.Now().UTC().Add(1 * time.Second)

	pid, acquiredAt, err := ReadLockInfo(path)
	if err != nil {
		t.Fatalf("ReadLockInfo: %v", err)
	}

	if pid != os.Getpid() {
		t.Errorf("PID = %d; want %d", pid, os.Getpid())
	}

	if acquiredAt.Before(before) || acquiredAt.After(after) {
		t.Errorf("acquiredAt = %s; expected between %s and %s", acquiredAt, before, after)
	}
}

func TestReadLockInfo_NonExistentFile(t *testing.T) {
	_, _, err := ReadLockInfo("/nonexistent/path/lock")
	if err == nil {
		t.Fatal("expected error for nonexistent lockfile")
	}
}
