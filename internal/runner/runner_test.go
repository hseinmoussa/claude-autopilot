package runner

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRotateLogIfNeeded(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "task.log")
	if err := os.WriteFile(path, []byte("1234567890"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := rotateLogIfNeeded(path, 1); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatalf("expected rotated backup: %v", err)
	}
}

func TestFormatTaskDuration(t *testing.T) {
	start := time.Now().Add(-5 * time.Second)
	end := time.Now()
	got := formatTaskDuration(&start, &end)
	if got == "n/a" {
		t.Fatalf("expected duration, got %q", got)
	}
}
