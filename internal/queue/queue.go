package queue

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/hseinmoussa/claude-autopilot/internal/fileutil"
	"gopkg.in/yaml.v3"
)

var taskIDRe = regexp.MustCompile(`^[a-z0-9-]+$`)

// IsValidID reports whether id matches the filesystem-safe task ID format.
func IsValidID(id string) bool {
	return len(id) > 0 && len(id) <= 64 && taskIDRe.MatchString(id)
}

// LoadTasks loads and merges task definitions from the global and project
// task directories. Tasks are validated, assigned defaults, de-duplicated,
// and returned sorted by (priority ASC, created_at ASC, id ASC).
func LoadTasks(globalDir, projectDir string) ([]Task, error) {
	tasks, _, err := LoadTasksAndInit(globalDir, projectDir, "")
	return tasks, err
}

// LoadTasksAndInit loads tasks like LoadTasks, and when stateDir is non-empty
// it also ensures/reads each task's immutable init record so created_at is
// canonicalized before sorting. The second return value is the count of newly
// initialized tasks.
func LoadTasksAndInit(globalDir, projectDir, stateDir string) ([]Task, int, error) {
	var allTasks []Task
	initCount := 0

	// Load from global source group.
	tasks, err := loadTaskSourceGroup(globalDir)
	if err != nil {
		return nil, 0, fmt.Errorf("load global tasks from %s: %w", globalDir, err)
	}
	allTasks = append(allTasks, tasks...)

	// Load from project source group if provided.
	if projectDir != "" {
		tasks, err := loadTaskSourceGroup(projectDir)
		if err != nil {
			return nil, 0, fmt.Errorf("load project tasks from %s: %w", projectDir, err)
		}
		allTasks = append(allTasks, tasks...)
	}

	// Canonicalize created_at from per-task init records before sorting.
	if stateDir != "" {
		for i := range allTasks {
			created, err := EnsureInit(stateDir, &allTasks[i])
			if err != nil {
				return nil, 0, fmt.Errorf("initialize task %q: %w", allTasks[i].ID, err)
			}
			if created {
				initCount++
			}
		}
	}

	// Detect duplicate IDs across all sources.
	seen := make(map[string]string) // id -> source
	for _, t := range allTasks {
		if prev, ok := seen[t.ID]; ok {
			return nil, 0, fmt.Errorf("Duplicate task ID '%s' found in %s and %s. Remove one.", t.ID, prev, t.Source)
		}
		seen[t.ID] = t.Source
	}

	// Sort: priority ASC, created_at ASC, id ASC.
	sort.Slice(allTasks, func(i, j int) bool {
		if allTasks[i].Priority != allTasks[j].Priority {
			return allTasks[i].Priority < allTasks[j].Priority
		}
		if !allTasks[i].CreatedAt.Equal(allTasks[j].CreatedAt) {
			return allTasks[i].CreatedAt.Before(allTasks[j].CreatedAt)
		}
		return allTasks[i].ID < allTasks[j].ID
	})

	return allTasks, initCount, nil
}

// loadTaskSourceGroup loads:
//  1. all YAML files in taskDir
//  2. companion multi-task files beside the task dir:
//     <parent>/tasks.yaml and <parent>/tasks.yml
func loadTaskSourceGroup(taskDir string) ([]Task, error) {
	var all []Task

	byDir, err := loadTasksFromDir(taskDir)
	if err != nil {
		return nil, err
	}
	all = append(all, byDir...)

	parent := filepath.Dir(taskDir)
	for _, name := range []string{"tasks.yaml", "tasks.yml"} {
		companion := filepath.Join(parent, name)
		byFile, err := loadTasksFromFile(companion)
		if err != nil {
			return nil, err
		}
		all = append(all, byFile...)
	}

	return all, nil
}

// loadTasksFromDir loads tasks from all *.yaml files in a directory, plus
// tasks.yaml as a multi-document file. Non-existent directories are silently
// skipped.
func loadTasksFromDir(dir string) ([]Task, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var allTasks []Task

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".yaml") && !strings.HasSuffix(name, ".yml") {
			continue
		}

		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}

		// All YAML files support multi-document format (--- separators).
		tasks, err := ParseMultiDocYAML(data, path)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}

		allTasks = append(allTasks, tasks...)
	}

	return allTasks, nil
}

func loadTasksFromFile(path string) ([]Task, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	tasks, err := ParseMultiDocYAML(data, path)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return tasks, nil
}

// ParseMultiDocYAML splits YAML data on "---" document separators and parses
// each document as a Task. Empty documents are skipped. The source string is
// attached to each parsed task for provenance tracking.
func ParseMultiDocYAML(data []byte, source string) ([]Task, error) {
	docs := splitYAMLDocs(data)
	var tasks []Task

	for i, doc := range docs {
		doc = bytes.TrimSpace(doc)
		if len(doc) == 0 {
			continue
		}

		var t Task
		if err := yaml.Unmarshal(doc, &t); err != nil {
			return nil, fmt.Errorf("document %d: %w", i+1, err)
		}

		// Keep doc provenance distinct for clearer duplicate/validation errors.
		if len(docs) > 1 {
			t.Source = fmt.Sprintf("%s#doc%d", source, i+1)
		} else {
			t.Source = source
		}

		// Apply defaults and auto-generate missing fields.
		if err := applyDefaults(&t); err != nil {
			return nil, fmt.Errorf("document %d: %w", i+1, err)
		}

		// Validate required fields.
		if err := validateTask(&t); err != nil {
			return nil, fmt.Errorf("document %d: %w", i+1, err)
		}

		tasks = append(tasks, t)
	}

	return tasks, nil
}

// splitYAMLDocs splits raw YAML bytes on "---" document separators.
func splitYAMLDocs(data []byte) [][]byte {
	// Split on lines that are exactly "---" (with optional leading/trailing whitespace).
	sep := regexp.MustCompile(`(?m)^---\s*$`)
	parts := sep.Split(string(data), -1)

	docs := make([][]byte, 0, len(parts))
	for _, p := range parts {
		docs = append(docs, []byte(p))
	}
	return docs
}

// applyDefaults fills in auto-generated and default values for a Task.
func applyDefaults(t *Task) error {
	// Auto-generate title from prompt if missing.
	if t.Title == "" && t.Prompt != "" {
		t.Title = truncate(t.Prompt, 60)
	}

	// Auto-generate ID from title if missing.
	if t.ID == "" {
		if t.Title == "" {
			return fmt.Errorf("task has no id, title, or prompt for ID generation")
		}
		t.ID = GenerateID(t.Title)
	}

	// Default priority.
	if t.Priority == 0 {
		t.Priority = 10
	}

	// Default max retries.
	if t.MaxRetries == 0 {
		t.MaxRetries = 5
	}

	return nil
}

// validateTask checks that required fields are present and valid.
func validateTask(t *Task) error {
	label := t.ID
	if strings.TrimSpace(label) == "" {
		label = "<unknown>"
	}
	if strings.TrimSpace(t.ID) == "" {
		return fmt.Errorf("Task '%s' (%s): missing required field 'id'", label, t.Source)
	}
	if len(t.ID) > 64 {
		return fmt.Errorf("Task '%s' (%s): id must be <= 64 characters", label, t.Source)
	}
	if !taskIDRe.MatchString(t.ID) {
		return fmt.Errorf("Task '%s' (%s): id must match [a-z0-9-]", label, t.Source)
	}
	if strings.TrimSpace(t.Prompt) == "" {
		return fmt.Errorf("Task '%s' (%s): missing required field 'prompt'", label, t.Source)
	}
	if strings.TrimSpace(t.WorkingDir) == "" {
		return fmt.Errorf("Task '%s' (%s): missing required field 'working_dir'", label, t.Source)
	}
	if !filepath.IsAbs(t.WorkingDir) {
		return fmt.Errorf("Task '%s': working_dir must be absolute (got '%s'). Use 'add --dir' which resolves automatically.", label, t.WorkingDir)
	}
	return nil
}

// truncate returns the first n characters of s, trimming to the last space
// boundary if possible to avoid mid-word cuts.
func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	// Replace newlines with spaces.
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' {
			return ' '
		}
		return r
	}, s)

	if len(s) <= n {
		return s
	}

	truncated := s[:n]
	// Try to break at last space.
	if idx := strings.LastIndex(truncated, " "); idx > n/2 {
		truncated = truncated[:idx]
	}
	return truncated
}

// Slugify converts a string to a URL-safe slug: lowercase, non-alphanumeric
// characters replaced with dashes, consecutive dashes collapsed, leading and
// trailing dashes removed, and truncated to a maximum of 64 characters.
func Slugify(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	prevDash := false
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			prevDash = false
		} else if !prevDash && b.Len() > 0 {
			b.WriteRune('-')
			prevDash = true
		}
	}

	result := strings.TrimRight(b.String(), "-")
	if len(result) > 64 {
		result = result[:64]
		result = strings.TrimRight(result, "-")
	}
	return result
}

// GenerateID creates a task ID by slugifying the title and appending 4 random
// hex characters for uniqueness. The total length is capped at 64 characters.
func GenerateID(title string) string {
	slug := Slugify(title)
	if slug == "" {
		slug = "task"
	}

	b := make([]byte, 2)
	rand.Read(b)
	suffix := hex.EncodeToString(b)

	// Reserve 5 chars for "-" + 4 hex suffix to stay within 64 char limit.
	const suffixLen = 5 // "-" + 4 hex chars
	maxSlug := 64 - suffixLen
	if len(slug) > maxSlug {
		slug = slug[:maxSlug]
		slug = strings.TrimRight(slug, "-")
	}

	return slug + "-" + suffix
}

// LoadState reads the TaskState for a given task ID from the state directory.
// The state file is expected at <stateDir>/<taskID>.state.json.
func LoadState(stateDir, taskID string) (*TaskState, error) {
	path := filepath.Join(stateDir, taskID+".state.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read state file %s: %w", path, err)
	}

	var state TaskState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("parse state file %s: %w", path, err)
	}
	return &state, nil
}

// SaveState writes the TaskState to disk using an atomic write for crash safety.
// The state file is written to <stateDir>/<state.ID>.state.json.
func SaveState(stateDir string, state *TaskState) error {
	path := filepath.Join(stateDir, state.ID+".state.json")
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal state for %s: %w", state.ID, err)
	}
	data = append(data, '\n')
	return fileutil.AtomicWrite(path, data, 0644)
}

// LoadInit reads the TaskInit record for a given task ID from the state directory.
// The init file is expected at <stateDir>/<taskID>.init.json.
func LoadInit(stateDir, taskID string) (*TaskInit, error) {
	path := filepath.Join(stateDir, taskID+".init.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read init file %s: %w", path, err)
	}

	var init TaskInit
	if err := json.Unmarshal(data, &init); err != nil {
		return nil, fmt.Errorf("parse init file %s: %w", path, err)
	}
	return &init, nil
}

// EnsureInit creates the init record for a task if it does not already exist.
// Uses AtomicCreate (hardlink) for race-safe create-once semantics. If the
// task's CreatedAt is zero, it is set to the current time.
func EnsureInit(stateDir string, task *Task) (bool, error) {
	if task.CreatedAt.IsZero() {
		task.CreatedAt = time.Now().UTC()
	}

	init := TaskInit{
		ID:        task.ID,
		CreatedAt: task.CreatedAt,
	}

	data, err := json.MarshalIndent(init, "", "  ")
	if err != nil {
		return false, fmt.Errorf("marshal init for %s: %w", task.ID, err)
	}
	data = append(data, '\n')

	path := filepath.Join(stateDir, task.ID+".init.json")
	created, err := fileutil.AtomicCreate(path, data, 0644)
	if err != nil {
		return false, fmt.Errorf("create init file for %s: %w", task.ID, err)
	}

	// If the file already existed, load the existing init to get the
	// canonical created_at timestamp.
	if !created {
		existing, err := LoadInit(stateDir, task.ID)
		if err != nil {
			return false, err
		}
		if existing != nil {
			task.CreatedAt = existing.CreatedAt
		}
	}

	return created, nil
}
