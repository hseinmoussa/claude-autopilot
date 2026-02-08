package fileutil

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// TempFileName
// ---------------------------------------------------------------------------

func TestTempFileName_Format(t *testing.T) {
	path := "/some/dir/state.json"
	name := TempFileName(path)

	// Expected: /some/dir/state.json.tmp.<pid>.<8hex>
	re := regexp.MustCompile(`^/some/dir/state\.json\.tmp\.\d+\.[0-9a-f]{8}$`)
	if !re.MatchString(name) {
		t.Fatalf("TempFileName(%q) = %q; does not match expected pattern", path, name)
	}
}

func TestTempFileName_ContainsPID(t *testing.T) {
	name := TempFileName("/x/y")
	expected := fmt.Sprintf(".tmp.%d.", os.Getpid())
	if !regexp.MustCompile(regexp.QuoteMeta(expected)).MatchString(name) {
		t.Errorf("TempFileName does not contain current PID (%d): %s", os.Getpid(), name)
	}
}

func TestTempFileName_UniquePerCall(t *testing.T) {
	a := TempFileName("/x")
	b := TempFileName("/x")
	if a == b {
		t.Fatalf("two calls returned same name: %s", a)
	}
}

// ---------------------------------------------------------------------------
// AtomicWrite
// ---------------------------------------------------------------------------

func TestAtomicWrite_WritesCorrectly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.txt")
	data := []byte("hello atomic world")

	if err := AtomicWrite(path, data, 0644); err != nil {
		t.Fatalf("AtomicWrite: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != string(data) {
		t.Errorf("file contents = %q; want %q", got, data)
	}
}

func TestAtomicWrite_CreatesParentDirs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a", "b", "c", "file.json")

	if err := AtomicWrite(path, []byte("ok"), 0644); err != nil {
		t.Fatalf("AtomicWrite should create parent dirs: %v", err)
	}

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("file not created: %v", err)
	}
}

func TestAtomicWrite_NoPartialFileVisible(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "target.txt")

	// Write the file.
	if err := AtomicWrite(path, []byte("complete"), 0644); err != nil {
		t.Fatalf("AtomicWrite: %v", err)
	}

	// After AtomicWrite returns, there should be no .tmp. files left.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if regexp.MustCompile(`\.tmp\.`).MatchString(e.Name()) {
			t.Errorf("temp file left behind: %s", e.Name())
		}
	}
}

func TestAtomicWrite_OverwriteExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")

	if err := AtomicWrite(path, []byte("v1"), 0644); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if err := AtomicWrite(path, []byte("v2"), 0644); err != nil {
		t.Fatalf("second write: %v", err)
	}

	got, _ := os.ReadFile(path)
	if string(got) != "v2" {
		t.Errorf("file = %q; want %q", got, "v2")
	}
}

// ---------------------------------------------------------------------------
// AtomicCreate
// ---------------------------------------------------------------------------

func TestAtomicCreate_CreatesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "init.json")

	created, err := AtomicCreate(path, []byte(`{"ok":true}`), 0644)
	if err != nil {
		t.Fatalf("AtomicCreate: %v", err)
	}
	if !created {
		t.Fatal("AtomicCreate returned false on fresh file")
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != `{"ok":true}` {
		t.Errorf("contents = %q; want %q", got, `{"ok":true}`)
	}
}

func TestAtomicCreate_SecondCallReturnsFalse(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "init.json")

	created, err := AtomicCreate(path, []byte("first"), 0644)
	if err != nil {
		t.Fatalf("first AtomicCreate: %v", err)
	}
	if !created {
		t.Fatal("first call should return true")
	}

	created2, err := AtomicCreate(path, []byte("second"), 0644)
	if err != nil {
		t.Fatalf("second AtomicCreate: %v", err)
	}
	if created2 {
		t.Fatal("second call should return false (file already exists)")
	}

	// Original contents should be preserved.
	got, _ := os.ReadFile(path)
	if string(got) != "first" {
		t.Errorf("contents = %q; want %q (original should be preserved)", got, "first")
	}
}

func TestAtomicCreate_CreatesParentDirs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "deep", "nested", "init.json")

	created, err := AtomicCreate(path, []byte("data"), 0644)
	if err != nil {
		t.Fatalf("AtomicCreate with nested dirs: %v", err)
	}
	if !created {
		t.Fatal("should have created file")
	}
}

// ---------------------------------------------------------------------------
// CleanOrphanTemps
// ---------------------------------------------------------------------------

func TestCleanOrphanTemps_DeletesDeadPIDTemps(t *testing.T) {
	dir := t.TempDir()

	// PID 999999999 almost certainly doesn't exist.
	deadFile := filepath.Join(dir, "state.json.tmp.999999999.abcd1234")
	if err := os.WriteFile(deadFile, []byte("orphan"), 0644); err != nil {
		t.Fatal(err)
	}

	cleaned, err := CleanOrphanTemps([]string{dir})
	if err != nil {
		t.Fatalf("CleanOrphanTemps: %v", err)
	}
	if cleaned != 1 {
		t.Errorf("cleaned = %d; want 1", cleaned)
	}
	if _, err := os.Stat(deadFile); !os.IsNotExist(err) {
		t.Error("dead-PID temp file should have been deleted")
	}
}

func TestCleanOrphanTemps_SkipsAlivePIDTemps(t *testing.T) {
	dir := t.TempDir()

	// Use current process PID (alive).
	aliveFile := filepath.Join(dir, fmt.Sprintf("state.json.tmp.%d.abcd1234", os.Getpid()))
	if err := os.WriteFile(aliveFile, []byte("active"), 0644); err != nil {
		t.Fatal(err)
	}

	cleaned, err := CleanOrphanTemps([]string{dir})
	if err != nil {
		t.Fatalf("CleanOrphanTemps: %v", err)
	}
	if cleaned != 0 {
		t.Errorf("cleaned = %d; want 0 (alive PID should be skipped)", cleaned)
	}
	if _, err := os.Stat(aliveFile); err != nil {
		t.Error("alive-PID temp file should NOT have been deleted")
	}
}

func TestCleanOrphanTemps_DeletesOldTemps(t *testing.T) {
	dir := t.TempDir()

	// Use current PID (alive) but set mtime to >24h ago.
	oldFile := filepath.Join(dir, fmt.Sprintf("data.tmp.%d.ffff0000", os.Getpid()))
	if err := os.WriteFile(oldFile, []byte("stale"), 0644); err != nil {
		t.Fatal(err)
	}
	oldTime := time.Now().Add(-25 * time.Hour)
	if err := os.Chtimes(oldFile, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}

	cleaned, err := CleanOrphanTemps([]string{dir})
	if err != nil {
		t.Fatalf("CleanOrphanTemps: %v", err)
	}
	if cleaned != 1 {
		t.Errorf("cleaned = %d; want 1 (old file should be deleted)", cleaned)
	}
}

func TestCleanOrphanTemps_SkipsNonExistentDirs(t *testing.T) {
	cleaned, err := CleanOrphanTemps([]string{"/nonexistent/dir/xyz"})
	if err != nil {
		t.Fatalf("CleanOrphanTemps should skip nonexistent dirs: %v", err)
	}
	if cleaned != 0 {
		t.Errorf("cleaned = %d; want 0", cleaned)
	}
}

func TestCleanOrphanTemps_SkipsNonTempFiles(t *testing.T) {
	dir := t.TempDir()

	regular := filepath.Join(dir, "normal_file.json")
	if err := os.WriteFile(regular, []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}

	cleaned, err := CleanOrphanTemps([]string{dir})
	if err != nil {
		t.Fatalf("CleanOrphanTemps: %v", err)
	}
	if cleaned != 0 {
		t.Errorf("cleaned = %d; want 0 (non-temp files should be skipped)", cleaned)
	}
	if _, err := os.Stat(regular); err != nil {
		t.Error("regular file should NOT have been deleted")
	}
}

// ---------------------------------------------------------------------------
// extractPID
// ---------------------------------------------------------------------------

func TestExtractPID(t *testing.T) {
	tests := []struct {
		name string
		want int
	}{
		{"state.json.tmp.12345.abcd", 12345},
		{"data.tmp.1.ff", 1},
		{"file.tmp.999999.00001234", 999999},
		{"notatemp", 0},
		{"file.tmp.", 0},
		{"file.tmp.notanumber.abc", 0},
		{".tmp.42.dead", 42},
	}

	for _, tt := range tests {
		got := extractPID(tt.name)
		if got != tt.want {
			t.Errorf("extractPID(%q) = %d; want %d", tt.name, got, tt.want)
		}
	}
}
