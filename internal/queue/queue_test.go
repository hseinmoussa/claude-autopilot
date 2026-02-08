package queue

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// LoadTasks
// ---------------------------------------------------------------------------

func TestLoadTasks_LoadsFromDirectory(t *testing.T) {
	dir := t.TempDir()

	writeYAML(t, filepath.Join(dir, "a.yaml"), `
id: task-a
title: Task A
priority: 5
prompt: do thing A
working_dir: /tmp
`)
	writeYAML(t, filepath.Join(dir, "b.yaml"), `
id: task-b
title: Task B
priority: 1
prompt: do thing B
working_dir: /tmp
`)

	tasks, err := LoadTasks(dir, "")
	if err != nil {
		t.Fatalf("LoadTasks: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("got %d tasks; want 2", len(tasks))
	}
}

func TestLoadTasks_SortsByPriorityCreatedAtID(t *testing.T) {
	dir := t.TempDir()

	now := time.Now().UTC()
	earlier := now.Add(-1 * time.Hour)

	writeYAML(t, filepath.Join(dir, "tasks.yaml"), `
id: z-task
title: Z Task
priority: 1
created_at: `+earlier.Format(time.RFC3339)+`
prompt: prompt z
working_dir: /tmp
---
id: a-task
title: A Task
priority: 1
created_at: `+now.Format(time.RFC3339)+`
prompt: prompt a
working_dir: /tmp
---
id: high-pri
title: High Priority
priority: 5
prompt: prompt high
working_dir: /tmp
`)

	tasks, err := LoadTasks(dir, "")
	if err != nil {
		t.Fatalf("LoadTasks: %v", err)
	}
	if len(tasks) != 3 {
		t.Fatalf("got %d tasks; want 3", len(tasks))
	}

	// priority 1 tasks come before priority 5
	if tasks[0].ID != "z-task" {
		t.Errorf("tasks[0].ID = %q; want z-task (lowest priority, earlier created_at)", tasks[0].ID)
	}
	if tasks[1].ID != "a-task" {
		t.Errorf("tasks[1].ID = %q; want a-task (lowest priority, later created_at)", tasks[1].ID)
	}
	if tasks[2].ID != "high-pri" {
		t.Errorf("tasks[2].ID = %q; want high-pri (priority 5)", tasks[2].ID)
	}
}

func TestLoadTasks_DuplicateIDError(t *testing.T) {
	globalDir := t.TempDir()
	projectDir := t.TempDir()

	writeYAML(t, filepath.Join(globalDir, "a.yaml"), `
id: dup-id
title: Global Task
prompt: prompt
working_dir: /tmp
`)
	writeYAML(t, filepath.Join(projectDir, "b.yaml"), `
id: dup-id
title: Project Task
prompt: prompt
working_dir: /tmp
`)

	_, err := LoadTasks(globalDir, projectDir)
	if err == nil {
		t.Fatal("expected duplicate ID error, got nil")
	}
	if !strings.Contains(err.Error(), "Duplicate task ID") {
		t.Errorf("error = %v; want duplicate task ID error", err)
	}
}

func TestLoadTasks_SkipsNonExistentDir(t *testing.T) {
	tasks, err := LoadTasks("/nonexistent/global", "/nonexistent/project")
	if err != nil {
		t.Fatalf("LoadTasks should skip nonexistent dirs: %v", err)
	}
	if len(tasks) != 0 {
		t.Errorf("got %d tasks; want 0", len(tasks))
	}
}

func TestLoadTasks_LoadsCompanionTasksYAML(t *testing.T) {
	base := t.TempDir()
	taskDir := filepath.Join(base, "tasks")

	writeYAML(t, filepath.Join(taskDir, "from-dir.yaml"), `
id: from-dir
prompt: dir task
working_dir: /tmp
`)
	writeYAML(t, filepath.Join(base, "tasks.yaml"), `
id: from-companion
prompt: companion task
working_dir: /tmp
`)

	tasks, err := LoadTasks(taskDir, "")
	if err != nil {
		t.Fatalf("LoadTasks: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("got %d tasks; want 2", len(tasks))
	}
}

func TestLoadTasksAndInit_UsesCanonicalCreatedAtFromInit(t *testing.T) {
	base := t.TempDir()
	taskDir := filepath.Join(base, "tasks")
	stateDir := filepath.Join(base, "state")
	if err := os.MkdirAll(stateDir, 0755); err != nil {
		t.Fatal(err)
	}

	writeYAML(t, filepath.Join(taskDir, "a.yaml"), `
id: task-a
priority: 1
prompt: a
working_dir: /tmp
`)
	writeYAML(t, filepath.Join(taskDir, "b.yaml"), `
id: task-b
priority: 1
prompt: b
working_dir: /tmp
`)

	old := time.Now().UTC().Add(-2 * time.Hour).Format(time.RFC3339)
	if err := os.WriteFile(filepath.Join(stateDir, "task-b.init.json"), []byte(`{"id":"task-b","created_at":"`+old+`"}`), 0644); err != nil {
		t.Fatal(err)
	}

	tasks, initCount, err := LoadTasksAndInit(taskDir, "", stateDir)
	if err != nil {
		t.Fatalf("LoadTasksAndInit: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("got %d tasks; want 2", len(tasks))
	}
	if initCount != 1 {
		t.Fatalf("initCount = %d; want 1", initCount)
	}
	if tasks[0].ID != "task-b" {
		t.Fatalf("tasks[0].ID = %q; want task-b (older init created_at)", tasks[0].ID)
	}
}

// ---------------------------------------------------------------------------
// ParseMultiDocYAML
// ---------------------------------------------------------------------------

func TestParseMultiDocYAML_SplitsDocuments(t *testing.T) {
	data := []byte(`
id: task-1
title: First
prompt: do first
working_dir: /tmp
---
id: task-2
title: Second
prompt: do second
working_dir: /tmp
`)
	tasks, err := ParseMultiDocYAML(data, "test.yaml")
	if err != nil {
		t.Fatalf("ParseMultiDocYAML: %v", err)
	}
	if len(tasks) != 2 {
		t.Fatalf("got %d tasks; want 2", len(tasks))
	}
	if tasks[0].ID != "task-1" {
		t.Errorf("tasks[0].ID = %q; want task-1", tasks[0].ID)
	}
	if tasks[1].ID != "task-2" {
		t.Errorf("tasks[1].ID = %q; want task-2", tasks[1].ID)
	}
}

func TestParseMultiDocYAML_SkipsEmptyDocuments(t *testing.T) {
	data := []byte(`
---

---
id: only-task
title: Only
prompt: do it
working_dir: /tmp
---
`)
	tasks, err := ParseMultiDocYAML(data, "test.yaml")
	if err != nil {
		t.Fatalf("ParseMultiDocYAML: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("got %d tasks; want 1", len(tasks))
	}
}

func TestParseMultiDocYAML_SetsSource(t *testing.T) {
	data := []byte(`
id: sourced
title: A Task
prompt: prompt
working_dir: /tmp
`)
	tasks, err := ParseMultiDocYAML(data, "/path/to/tasks.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if tasks[0].Source != "/path/to/tasks.yaml" {
		t.Errorf("Source = %q; want /path/to/tasks.yaml", tasks[0].Source)
	}
}

func TestParseMultiDocYAML_ValidationErrors(t *testing.T) {
	// Missing prompt.
	data := []byte(`
id: no-prompt
title: No Prompt
working_dir: /tmp
`)
	_, err := ParseMultiDocYAML(data, "test.yaml")
	if err == nil {
		t.Fatal("expected error for missing prompt")
	}
	if !strings.Contains(err.Error(), "missing required field 'prompt'") {
		t.Errorf("error = %v; want missing required prompt field", err)
	}
}

func TestParseMultiDocYAML_RelativeWorkingDir(t *testing.T) {
	data := []byte(`
id: rel-dir
title: Relative
prompt: do it
working_dir: relative/path
`)
	_, err := ParseMultiDocYAML(data, "test.yaml")
	if err == nil {
		t.Fatal("expected error for relative working_dir")
	}
	if !strings.Contains(err.Error(), "must be absolute") {
		t.Errorf("error = %v; want absolute path error", err)
	}
}

func TestParseMultiDocYAML_DefaultPriority(t *testing.T) {
	data := []byte(`
id: no-priority
title: NoPri
prompt: do it
working_dir: /tmp
`)
	tasks, err := ParseMultiDocYAML(data, "test.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if tasks[0].Priority != 10 {
		t.Errorf("default priority = %d; want 10", tasks[0].Priority)
	}
}

func TestParseMultiDocYAML_DefaultMaxRetries(t *testing.T) {
	data := []byte(`
id: no-retries
title: NoRetry
prompt: do it
working_dir: /tmp
`)
	tasks, err := ParseMultiDocYAML(data, "test.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if tasks[0].MaxRetries != 5 {
		t.Errorf("default max_retries = %d; want 5", tasks[0].MaxRetries)
	}
}

// ---------------------------------------------------------------------------
// Slugify
// ---------------------------------------------------------------------------

func TestSlugify(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Hello World", "hello-world"},
		{"Fix the BUG!", "fix-the-bug"},
		{"  leading trailing  ", "leading-trailing"},
		{"UPPER CASE", "upper-case"},
		{"special@chars#here", "special-chars-here"},
		{"no---multiple---dashes", "no-multiple-dashes"},
		{"", ""},
	}

	for _, tt := range tests {
		got := Slugify(tt.input)
		if got != tt.want {
			t.Errorf("Slugify(%q) = %q; want %q", tt.input, got, tt.want)
		}
	}
}

func TestSlugify_MaxLength(t *testing.T) {
	long := strings.Repeat("a", 100)
	got := Slugify(long)
	if len(got) > 64 {
		t.Errorf("Slugify result length = %d; want <= 64", len(got))
	}
}

// ---------------------------------------------------------------------------
// GenerateID
// ---------------------------------------------------------------------------

func TestGenerateID_Format(t *testing.T) {
	id := GenerateID("Build the feature")
	// Should be: <slug>-<4hex>
	re := regexp.MustCompile(`^[a-z0-9-]+-[0-9a-f]{4}$`)
	if !re.MatchString(id) {
		t.Errorf("GenerateID = %q; does not match expected pattern", id)
	}
}

func TestGenerateID_UniquePerCall(t *testing.T) {
	a := GenerateID("same title")
	b := GenerateID("same title")
	if a == b {
		t.Errorf("two GenerateID calls returned same ID: %s", a)
	}
}

func TestGenerateID_EmptySlug(t *testing.T) {
	id := GenerateID("!!!") // Slugify produces ""
	if !strings.HasPrefix(id, "task-") {
		t.Errorf("GenerateID with empty slug = %q; want prefix 'task-'", id)
	}
}

func TestGenerateID_LongTitle_Within64Chars(t *testing.T) {
	longTitle := strings.Repeat("a very long title that exceeds the limit ", 5)
	id := GenerateID(longTitle)
	if len(id) > 64 {
		t.Errorf("GenerateID with long title produced id of length %d (> 64): %q", len(id), id)
	}
	if !IsValidID(id) {
		t.Errorf("GenerateID with long title produced invalid id: %q", id)
	}
}

func TestIsValidID(t *testing.T) {
	valid := []string{"abc", "a1-b2", "task-1234"}
	for _, id := range valid {
		if !IsValidID(id) {
			t.Fatalf("expected valid id: %s", id)
		}
	}

	invalid := []string{"", "UPPER", "contains_space", "ümlaut", strings.Repeat("a", 65)}
	for _, id := range invalid {
		if IsValidID(id) {
			t.Fatalf("expected invalid id: %s", id)
		}
	}
}

// ---------------------------------------------------------------------------
// SaveState / LoadState roundtrip
// ---------------------------------------------------------------------------

func TestSaveState_LoadState_Roundtrip(t *testing.T) {
	dir := t.TempDir()

	now := time.Now().UTC().Truncate(time.Second)
	state := &TaskState{
		ID:        "test-task-1234",
		Status:    StatusRunning,
		Attempt:   3,
		StartedAt: &now,
	}

	if err := SaveState(dir, state); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	loaded, err := LoadState(dir, "test-task-1234")
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if loaded == nil {
		t.Fatal("LoadState returned nil")
	}
	if loaded.ID != state.ID {
		t.Errorf("ID = %q; want %q", loaded.ID, state.ID)
	}
	if loaded.Status != state.Status {
		t.Errorf("Status = %q; want %q", loaded.Status, state.Status)
	}
	if loaded.Attempt != state.Attempt {
		t.Errorf("Attempt = %d; want %d", loaded.Attempt, state.Attempt)
	}
}

func TestLoadState_NonExistentReturnsNil(t *testing.T) {
	dir := t.TempDir()
	state, err := LoadState(dir, "nonexistent")
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if state != nil {
		t.Error("expected nil for nonexistent state")
	}
}

// ---------------------------------------------------------------------------
// EnsureInit
// ---------------------------------------------------------------------------

func TestEnsureInit_CreatesOnce(t *testing.T) {
	dir := t.TempDir()
	task := &Task{
		ID:         "init-test",
		Prompt:     "test",
		WorkingDir: "/tmp",
	}

	if _, err := EnsureInit(dir, task); err != nil {
		t.Fatalf("first EnsureInit: %v", err)
	}

	// Verify init file exists.
	path := filepath.Join(dir, "init-test.init.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile init: %v", err)
	}

	var init TaskInit
	if err := json.Unmarshal(data, &init); err != nil {
		t.Fatalf("unmarshal init: %v", err)
	}
	if init.ID != "init-test" {
		t.Errorf("init.ID = %q; want init-test", init.ID)
	}
	origTime := init.CreatedAt

	// Second call should read existing and preserve created_at.
	task2 := &Task{
		ID:         "init-test",
		Prompt:     "test",
		WorkingDir: "/tmp",
	}
	if _, err := EnsureInit(dir, task2); err != nil {
		t.Fatalf("second EnsureInit: %v", err)
	}
	if !task2.CreatedAt.Equal(origTime) {
		t.Errorf("second call CreatedAt = %s; want %s (should preserve original)", task2.CreatedAt, origTime)
	}
}

func TestEnsureInit_SetsCreatedAtIfZero(t *testing.T) {
	dir := t.TempDir()
	task := &Task{
		ID:         "zero-time",
		Prompt:     "test",
		WorkingDir: "/tmp",
	}

	before := time.Now().UTC().Add(-1 * time.Second)
	if _, err := EnsureInit(dir, task); err != nil {
		t.Fatal(err)
	}
	after := time.Now().UTC().Add(1 * time.Second)

	if task.CreatedAt.Before(before) || task.CreatedAt.After(after) {
		t.Errorf("CreatedAt = %s; expected between %s and %s", task.CreatedAt, before, after)
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func writeYAML(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}
