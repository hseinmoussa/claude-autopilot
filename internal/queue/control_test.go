package queue

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAppendCommand_ReadCommands_Roundtrip(t *testing.T) {
	dir := t.TempDir()
	now := time.Now().UTC().Truncate(time.Second)

	cmd1 := ControlCommand{
		Op:          "retry",
		TaskID:      "task-1",
		RequestedAt: now,
	}
	cmd2 := ControlCommand{
		Op:          "cancel",
		TaskID:      "task-2",
		RequestedAt: now.Add(1 * time.Second),
	}

	if err := AppendCommand(dir, cmd1); err != nil {
		t.Fatalf("AppendCommand 1: %v", err)
	}
	if err := AppendCommand(dir, cmd2); err != nil {
		t.Fatalf("AppendCommand 2: %v", err)
	}

	commands, err := ReadCommands(dir)
	if err != nil {
		t.Fatalf("ReadCommands: %v", err)
	}

	if len(commands) != 2 {
		t.Fatalf("got %d commands; want 2", len(commands))
	}
	if commands[0].Op != "retry" || commands[0].TaskID != "task-1" {
		t.Errorf("cmd[0] = %+v; want retry/task-1", commands[0])
	}
	if commands[1].Op != "cancel" || commands[1].TaskID != "task-2" {
		t.Errorf("cmd[1] = %+v; want cancel/task-2", commands[1])
	}
}

func TestClearCommands_EmptiesFile(t *testing.T) {
	dir := t.TempDir()

	cmd := ControlCommand{
		Op:          "retry",
		TaskID:      "task-1",
		RequestedAt: time.Now().UTC(),
	}
	if err := AppendCommand(dir, cmd); err != nil {
		t.Fatal(err)
	}

	if err := ClearCommands(dir); err != nil {
		t.Fatalf("ClearCommands: %v", err)
	}

	commands, err := ReadCommands(dir)
	if err != nil {
		t.Fatalf("ReadCommands after clear: %v", err)
	}
	if len(commands) != 0 {
		t.Errorf("got %d commands after clear; want 0", len(commands))
	}
}

func TestClearCommands_NoFileIsNoop(t *testing.T) {
	dir := t.TempDir()

	if err := ClearCommands(dir); err != nil {
		t.Fatalf("ClearCommands on empty dir: %v", err)
	}
}

func TestReadCommands_NonExistentDir(t *testing.T) {
	commands, err := ReadCommands("/nonexistent/dir")
	if err != nil {
		t.Fatalf("ReadCommands should return nil for nonexistent: %v", err)
	}
	if commands != nil {
		t.Errorf("expected nil commands, got %v", commands)
	}
}

func TestReadCommands_MalformedLinesSkipped(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "commands.jsonl")

	// Write a mix of valid and malformed lines.
	content := `{"op":"retry","task_id":"task-1","requested_at":"2025-01-01T00:00:00Z"}
not valid json at all
{"op":"cancel","task_id":"task-2","requested_at":"2025-01-02T00:00:00Z"}
{malformed
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	commands, err := ReadCommands(dir)
	if err != nil {
		t.Fatalf("ReadCommands: %v", err)
	}

	// Should have 2 valid commands (malformed lines skipped).
	if len(commands) != 2 {
		t.Fatalf("got %d commands; want 2 (malformed should be skipped)", len(commands))
	}
	if commands[0].TaskID != "task-1" {
		t.Errorf("commands[0].TaskID = %q; want task-1", commands[0].TaskID)
	}
	if commands[1].TaskID != "task-2" {
		t.Errorf("commands[1].TaskID = %q; want task-2", commands[1].TaskID)
	}
}

func TestAppendCommand_CreatesDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "control")

	cmd := ControlCommand{
		Op:          "retry",
		TaskID:      "task-1",
		RequestedAt: time.Now().UTC(),
	}

	if err := AppendCommand(dir, cmd); err != nil {
		t.Fatalf("AppendCommand should create dirs: %v", err)
	}

	commands, err := ReadCommands(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(commands) != 1 {
		t.Fatalf("got %d commands; want 1", len(commands))
	}
}
