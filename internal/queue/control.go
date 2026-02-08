package queue

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"
)

// ControlCommand represents an operator-issued command to modify task state
// outside of normal execution flow (e.g., retry a failed task, cancel a
// running task).
type ControlCommand struct {
	Op          string    `json:"op"`
	TaskID      string    `json:"task_id"`
	RequestedAt time.Time `json:"requested_at"`
}

// AppendCommand appends a control command to the commands.jsonl file in the
// given control directory. The file is flock-protected so multiple writers
// (e.g., concurrent CLI invocations) can safely append.
func AppendCommand(controlDir string, cmd ControlCommand) error {
	if err := os.MkdirAll(controlDir, 0755); err != nil {
		return fmt.Errorf("create control directory %s: %w", controlDir, err)
	}

	path := filepath.Join(controlDir, "commands.jsonl")

	fd, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0644)
	if err != nil {
		return fmt.Errorf("open commands file %s: %w", path, err)
	}
	defer fd.Close()

	// Acquire exclusive lock for the duration of the append.
	if err := lockFileExclusive(fd); err != nil {
		return fmt.Errorf("lock commands file %s: %w", path, err)
	}
	defer unlockFile(fd)

	data, err := json.Marshal(cmd)
	if err != nil {
		return fmt.Errorf("marshal control command: %w", err)
	}
	data = append(data, '\n')

	if _, err := fd.Write(data); err != nil {
		return fmt.Errorf("write control command: %w", err)
	}

	if err := fd.Sync(); err != nil {
		return fmt.Errorf("fsync commands file: %w", err)
	}

	return nil
}

// ReadCommands reads all control commands from the commands.jsonl file.
// Malformed lines are logged as warnings and skipped.
func ReadCommands(controlDir string) ([]ControlCommand, error) {
	path := filepath.Join(controlDir, "commands.jsonl")

	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("open commands file %s: %w", path, err)
	}
	defer f.Close()

	var commands []ControlCommand
	scanner := bufio.NewScanner(f)
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var cmd ControlCommand
		if err := json.Unmarshal(line, &cmd); err != nil {
			log.Printf("warning: skipping malformed control command at %s:%d: %v", path, lineNum, err)
			continue
		}
		commands = append(commands, cmd)
	}

	if err := scanner.Err(); err != nil {
		return commands, fmt.Errorf("read commands file %s: %w", path, err)
	}

	return commands, nil
}

// ClearCommands truncates the commands.jsonl file, effectively removing all
// commands after they have been consumed and applied.
func ClearCommands(controlDir string) error {
	path := filepath.Join(controlDir, "commands.jsonl")

	// Truncate to zero length. If the file does not exist, this is a no-op.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("truncate commands file %s: %w", path, err)
	}
	defer f.Close()

	if err := f.Sync(); err != nil {
		return fmt.Errorf("fsync commands file after truncate: %w", err)
	}

	return nil
}
