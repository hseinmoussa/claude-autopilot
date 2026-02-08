package runner

import (
	"bufio"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"math/rand"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/hseinmoussa/claude-autopilot/internal/compat"
	"github.com/hseinmoussa/claude-autopilot/internal/config"
	"github.com/hseinmoussa/claude-autopilot/internal/detector"
	"github.com/hseinmoussa/claude-autopilot/internal/fileutil"
	"github.com/hseinmoussa/claude-autopilot/internal/lock"
	"github.com/hseinmoussa/claude-autopilot/internal/notifier"
	"github.com/hseinmoussa/claude-autopilot/internal/queue"
	"github.com/hseinmoussa/claude-autopilot/internal/resume"
)

// Exit codes returned by Run.
const (
	ExitOK     = 0
	ExitFailed = 1
	ExitFatal  = 2
	ExitSignal = 130
)

// NDJSONMessage is the envelope for parsing Claude Code's NDJSON output.
// Each line is a JSON object with a "type" field; the remaining fields vary
// by type and are captured in Rest for selective further parsing.
type NDJSONMessage struct {
	Type string          `json:"type"`
	Rest json.RawMessage `json:"-"`
}

// UnmarshalJSON implements custom JSON unmarshaling that captures the full
// raw message in Rest while extracting the Type field.
func (m *NDJSONMessage) UnmarshalJSON(data []byte) error {
	// Extract type field.
	var envelope struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return err
	}
	m.Type = envelope.Type
	m.Rest = json.RawMessage(data)
	return nil
}

// SystemMessage is emitted once at the start of a Claude Code stream.
type SystemMessage struct {
	SessionID string `json:"session_id"`
}

// ResultMessage signals that Claude Code has finished producing output.
type ResultMessage struct {
	// We only need to detect type="result"; no additional fields required.
}

// Runner is the core execution engine for claude-autopilot. It manages
// the task queue, spawns Claude Code subprocesses, and handles rate-limit
// detection, retries, and graceful shutdown.
type Runner struct {
	Config         *config.Config
	Adapter        compat.CLIAdapter
	Detector       *detector.Detector
	Notifier       *notifier.Notifier
	Lock           *lock.Lock
	ProjectDir     string
	YesFlag        bool
	PromptPatterns []string
	ShuttingDown   atomic.Bool

	// promptPatterns are used for hang detection when skip_permissions is false.
	promptPatterns []string
}

// Run is the main execution loop. It acquires the runner lock, loads tasks,
// and processes them sequentially in priority order. It returns an exit code
// suitable for os.Exit.
func (r *Runner) Run() int {
	runStarted := time.Now()

	// Step 1: Ensure directory structure.
	if err := config.EnsureDirs(); err != nil {
		log.Printf("ERROR: failed to create directories: %v", err)
		return ExitFatal
	}

	// Step 2: Acquire runner lock.
	lockPath := filepath.Join(config.BaseDir(), "runner.lock")
	lk, err := lock.AcquireLock(lockPath)
	if err != nil {
		if errors.Is(err, lock.ErrLocked) {
			fmt.Fprintf(os.Stderr, "Another claude-autopilot instance is already running.\n%v\n", err)
			return ExitFatal
		}
		log.Printf("ERROR: failed to acquire lock: %v", err)
		return ExitFatal
	}
	r.Lock = lk
	defer r.Lock.Release()

	// Step 3: Clean orphan temp files.
	base := config.BaseDir()
	cleanDirs := []string{
		base,
		filepath.Join(base, "state"),
		filepath.Join(base, "tasks"),
		filepath.Join(base, "control"),
	}
	if r.ProjectDir != "" {
		cleanDirs = append(cleanDirs, r.ProjectDir)
	}
	if n, err := fileutil.CleanOrphanTemps(cleanDirs); err != nil {
		log.Printf("WARN: orphan temp cleanup: %v", err)
	} else if n > 0 {
		log.Printf("Cleaned %d orphan temp file(s)", n)
	}

	r.promptPatterns = append([]string(nil), r.PromptPatterns...)

	// Step 4: First-run safety prompt.
	if !r.YesFlag && !r.checkFirstRun() {
		fmt.Fprintln(os.Stderr, "First-run acknowledgement declined. Exiting.")
		return ExitOK
	}

	// Setup signal handler for graceful shutdown.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sigCh
		r.ShuttingDown.Store(true)
	}()

	// Main loop.
	stateDir := filepath.Join(base, "state")
	controlDir := filepath.Join(base, "control")
	globalTaskDir := filepath.Join(base, "tasks")
	anyFailed := false

	for {
		if r.ShuttingDown.Load() {
			return ExitSignal
		}

		// Step 5: Process queued control commands.
		if err := r.processControlCommands(controlDir, stateDir); err != nil {
			log.Printf("WARN: processing control commands: %v", err)
		}

		// Step 6: Load all tasks.
		tasks, initCount, err := queue.LoadTasksAndInit(globalTaskDir, r.ProjectDir, stateDir)
		if err != nil {
			log.Printf("ERROR: loading tasks: %v", err)
			return ExitFatal
		}
		if initCount > 0 {
			fmt.Printf("ℹ Initialized state for %d new tasks\n", initCount)
		}

		if len(tasks) == 0 {
			fmt.Println("No tasks found. Add tasks with 'claude-autopilot add' or create YAML files in ~/.claude-autopilot/tasks/")
			return ExitOK
		}

		// Load states.
		states := make(map[string]*queue.TaskState, len(tasks))
		for i := range tasks {
			st, err := queue.LoadState(stateDir, tasks[i].ID)
			if err != nil {
				log.Printf("WARN: load state for %s: %v", tasks[i].ID, err)
			}
			if st == nil {
				st = &queue.TaskState{
					ID:     tasks[i].ID,
					Status: queue.StatusPending,
				}
			} else if st.Status == queue.StatusRunning {
				// Crash recovery: stale running tasks are put back to pending.
				st.Status = queue.StatusPending
				if err := queue.SaveState(stateDir, st); err != nil {
					log.Printf("WARN: crash recovery save for %s: %v", tasks[i].ID, err)
				}
			}
			states[tasks[i].ID] = st
		}

		// Step 7: Filter to actionable tasks.
		now := time.Now()
		var actionable []queue.Task
		var waitingFuture []queue.Task

		for _, t := range tasks {
			st := states[t.ID]
			switch st.Status {
			case queue.StatusPending:
				actionable = append(actionable, t)
			case queue.StatusWaiting:
				if st.ResumeAt != nil && st.ResumeAt.After(now) {
					waitingFuture = append(waitingFuture, t)
				} else {
					// resume_at elapsed or nil; treat as actionable
					actionable = append(actionable, t)
				}
			case queue.StatusDone:
				// already finished
			case queue.StatusFailed:
				anyFailed = true
			case queue.StatusCancelled:
				// skip
			}
		}

		// Step 8: Pick and execute highest-priority actionable task.
		if len(actionable) > 0 {
			task := actionable[0] // already sorted by priority
			st := states[task.ID]

			exitResult := r.executeTask(&task, st, stateDir)

			// Reload state after execution.
			st, _ = queue.LoadState(stateDir, task.ID)
			if st != nil && st.Status == queue.StatusFailed {
				anyFailed = true
			}

			if exitResult == ExitSignal {
				return ExitSignal
			}
			// Loop back to pick next task.
			continue
		}

		// Step 10: Only waiting tasks with future resume_at.
		if len(waitingFuture) > 0 {
			earliest := r.findEarliestResume(waitingFuture, states)
			if earliest == nil {
				// No valid resume times; exit.
				break
			}

			fmt.Printf("All tasks waiting. Next resume at %s\n", earliest.Format(time.RFC3339))

			// Sleep loop with short ticks.
			for {
				if r.ShuttingDown.Load() {
					return ExitSignal
				}

				until := time.Until(*earliest)
				if until <= 0 {
					break
				}

				tick := 30 * time.Second
				if until < tick {
					tick = until
				}

				r.showCountdown(*earliest, &waitingFuture[0], states[waitingFuture[0].ID].Attempt)
				time.Sleep(tick)

				// Process control commands on each tick.
				if err := r.processControlCommands(controlDir, stateDir); err != nil {
					log.Printf("WARN: processing control commands during wait: %v", err)
				}
				// Re-evaluate queue every tick.
				break
			}
			// After wait completes, loop back to pick tasks.
			continue
		}

		// Step 11: No actionable and no waiting tasks.
		break
	}

	// Print summary and exit.
	r.printSummary(stateDir, runStarted)

	if r.Notifier != nil {
		r.Notifier.NotifyComplete("claude-autopilot run completed")
	}

	if anyFailed {
		return ExitFailed
	}
	return ExitOK
}

// executeTask runs a single task through the Claude Code CLI and manages
// its lifecycle: pre-run state setup, subprocess execution, output parsing,
// result detection, and post-run state transitions.
func (r *Runner) executeTask(task *queue.Task, state *queue.TaskState, stateDir string) int {
	now := time.Now().UTC()

	if !filepath.IsAbs(task.WorkingDir) {
		log.Printf("ERROR: Task '%s': working_dir must be absolute (got '%s'). Use 'add --dir' which resolves automatically.", task.ID, task.WorkingDir)
		state.Status = queue.StatusFailed
		state.EndedAt = &now
		_ = queue.SaveState(stateDir, state)
		return ExitFailed
	}
	if info, err := os.Stat(task.WorkingDir); err != nil || !info.IsDir() {
		log.Printf("ERROR: task %s working_dir does not exist: %s", task.ID, task.WorkingDir)
		state.Status = queue.StatusFailed
		state.EndedAt = &now
		_ = queue.SaveState(stateDir, state)
		return ExitFailed
	}

	// Pre-run: set state to running.
	state.Status = queue.StatusRunning
	state.Attempt++
	state.StartedAt = &now
	state.EndedAt = nil
	state.PromptHash = hashPrompt(task.Prompt)
	state.GitCommit = r.currentGitCommit(task.WorkingDir)

	if err := queue.SaveState(stateDir, state); err != nil {
		log.Printf("ERROR: save pre-run state for %s: %v", task.ID, err)
		return ExitFatal
	}

	logDir := filepath.Join(config.BaseDir(), "logs")
	logPath := filepath.Join(logDir, fmt.Sprintf("%s.log", task.ID))
	if err := os.MkdirAll(logDir, 0755); err != nil {
		log.Printf("WARN: could not create log dir %s: %v", logDir, err)
	}
	if err := rotateLogIfNeeded(logPath, 10*1024*1024); err != nil {
		log.Printf("WARN: log rotation failed for %s: %v", logPath, err)
	}

	log.Printf("Running task %s (attempt %d): %s", task.ID, state.Attempt, task.Title)

	// Build the prompt, prepending context files if any.
	prompt, err := r.buildPromptWithContext(task, state)
	if err != nil {
		log.Printf("ERROR: build prompt for %s: %v", task.ID, err)
		state.Status = queue.StatusFailed
		now := time.Now().UTC()
		state.EndedAt = &now
		queue.SaveState(stateDir, state)
		return ExitFailed
	}

	// Determine session ID for resume.
	sessionID := ""
	if state.SessionID != "" && r.Adapter.SupportsResume() {
		strategy := resume.DetermineStrategy(true, r.Adapter.SupportsResume())
		if strategy == resume.NativeResume {
			sessionID = state.SessionID
		}
	}

	// Determine skip_permissions: task-level overrides global config.
	skipPerms := r.Config.SkipPermissions || task.SkipPermissions

	// Build CLI arguments.
	args := r.Adapter.BuildArgs(prompt, task.Model, sessionID, skipPerms, task.Flags)

	// Spawn subprocess.
	cmd := exec.Command("claude", args...)
	cmd.Dir = task.WorkingDir
	cmd.Env = os.Environ()

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		log.Printf("ERROR: create stdout pipe for %s: %v", task.ID, err)
		state.Status = queue.StatusFailed
		queue.SaveState(stateDir, state)
		return ExitFailed
	}

	var stderrBuf strings.Builder
	cmd.Stderr = &stderrBuf

	if err := cmd.Start(); err != nil {
		log.Printf("ERROR: start claude for %s: %v", task.ID, err)
		state.Status = queue.StatusFailed
		now := time.Now().UTC()
		state.EndedAt = &now
		queue.SaveState(stateDir, state)
		return ExitFailed
	}

	// Open per-task log file.
	logFile, logErr := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if logErr != nil {
		log.Printf("WARN: cannot open log file %s: %v", logPath, logErr)
	} else {
		fmt.Fprintf(logFile, "\n[%s] attempt=%d task=%s\n", time.Now().UTC().Format(time.RFC3339), state.Attempt, task.ID)
	}
	defer func() {
		if logFile != nil {
			logFile.Close()
		}
	}()

	// Read stdout line by line.
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024) // 1MB line buffer

	var stdoutBuf strings.Builder
	var lastLines []string
	const maxLastLines = 20
	lastOutputTime := time.Now()
	var lastOutputMu sync.Mutex
	streamJSON := r.Adapter.SupportsStreamJSON()
	gotResult := false

	// Hang detection goroutine.
	hangTimeout := r.Config.HangTimeout
	if hangTimeout == 0 {
		hangTimeout = 10 * time.Minute
	}

	hangDone := make(chan struct{})
	defer close(hangDone)

	go func() {
		promptSilenceGate := 30 * time.Second
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-hangDone:
				return
			case <-ticker.C:
				if r.ShuttingDown.Load() {
					return
				}

				lastOutputMu.Lock()
				silence := time.Since(lastOutputTime)
				lastOutputMu.Unlock()

				// If skip_permissions is false, check for prompt-like patterns
				// with a shorter silence gate.
				if !skipPerms && len(r.promptPatterns) > 0 {
					lastOutputMu.Lock()
					recentOutput := strings.Join(lastLines, "\n")
					lastOutputMu.Unlock()
					if r.matchesPromptPattern(recentOutput) && silence >= promptSilenceGate {
						log.Printf("WARN: task %s appears stuck at permission prompt (silent for %v). Killing.", task.ID, silence)
						cmd.Process.Signal(syscall.SIGTERM)
						return
					}
				}

				// General hang timeout.
				if silence >= hangTimeout {
					log.Printf("WARN: task %s has produced no output for %v. Killing.", task.ID, silence)
					cmd.Process.Signal(syscall.SIGTERM)
					time.AfterFunc(10*time.Second, func() {
						cmd.Process.Kill()
					})
					return
				}
			}
		}
	}()

	// Graceful shutdown handler for subprocess.
	shutdownDone := make(chan struct{})
	defer close(shutdownDone)

	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-shutdownDone:
				return
			case <-ticker.C:
				if r.ShuttingDown.Load() {
					log.Printf("Shutdown signal received; terminating task %s", task.ID)
					cmd.Process.Signal(syscall.SIGTERM)
					// Wait up to 10s then SIGKILL.
					time.AfterFunc(10*time.Second, func() {
						cmd.Process.Kill()
					})
					return
				}
			}
		}
	}()

	for scanner.Scan() {
		line := scanner.Text()

		lastOutputMu.Lock()
		lastOutputTime = time.Now()
		lastOutputMu.Unlock()

		stdoutBuf.WriteString(line)
		stdoutBuf.WriteString("\n")

		// Keep last N lines for resume context.
		lastOutputMu.Lock()
		lastLines = append(lastLines, line)
		if len(lastLines) > maxLastLines {
			lastLines = lastLines[len(lastLines)-maxLastLines:]
		}
		lastOutputMu.Unlock()

		// Log to per-task log file.
		if logFile != nil {
			fmt.Fprintln(logFile, line)
		}

		// Parse NDJSON if supported.
		if streamJSON {
			var msg NDJSONMessage
			if err := json.Unmarshal([]byte(line), &msg); err == nil {
				switch msg.Type {
				case "system":
					var sysMsg SystemMessage
					if err := json.Unmarshal(msg.Rest, &sysMsg); err == nil && sysMsg.SessionID != "" {
						state.SessionID = sysMsg.SessionID
					}
				case "result":
					gotResult = true
				}
			}
		}
	}

	// Wait for process to exit.
	cmdErr := cmd.Wait()
	exitCode := 0
	if cmdErr != nil {
		var exitErr *exec.ExitError
		if errors.As(cmdErr, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = 1
		}
	}

	stderrStr := stderrBuf.String()
	stdoutStr := stdoutBuf.String()

	// Save last NDJSON messages for resume context.
	state.LastNDJSONMessages = lastLines

	// If we got a shutdown signal during execution, save state and return.
	if r.ShuttingDown.Load() {
		// Preserve running -> pending for clean restart.
		if state.Status == queue.StatusRunning {
			state.Status = queue.StatusPending
			state.Attempt-- // don't count interrupted attempt
			state.EndedAt = nil
		}
		queue.SaveState(stateDir, state)
		return ExitSignal
	}

	_ = gotResult // used for future enhancements

	// Run detection.
	result := r.Detector.Detect(exitCode, stdoutStr, stderrStr)

	log.Printf("Task %s exit_code=%d detection=%s reason=%q",
		task.ID, exitCode, result.Result, result.Reason)

	// Transition based on detection result.
	switch result.Result {
	case detector.Completed:
		state.Status = queue.StatusDone
		log.Printf("Task %s completed successfully", task.ID)

	case detector.RateLimited:
		state.Status = queue.StatusWaiting
		now := time.Now().UTC()
		state.LastRateLimitedAt = &now

		if result.ResetTime != nil {
			state.ResumeAt = result.ResetTime
			log.Printf("Task %s rate limited; resume at %s", task.ID, result.ResetTime.Format(time.RFC3339))
		} else {
			backoff := exponentialBackoff(state.Attempt)
			resumeAt := time.Now().Add(backoff)
			state.ResumeAt = &resumeAt
			log.Printf("Task %s rate limited; backoff %v, resume at %s", task.ID, backoff, resumeAt.Format(time.RFC3339))
		}

	case detector.Failed:
		if state.Attempt < task.MaxRetries {
			state.Status = queue.StatusWaiting
			backoff := exponentialBackoff(state.Attempt)
			resumeAt := time.Now().Add(backoff)
			state.ResumeAt = &resumeAt
			log.Printf("Task %s failed (attempt %d/%d); retry in %v",
				task.ID, state.Attempt, task.MaxRetries, backoff)
		} else {
			state.Status = queue.StatusFailed
			log.Printf("Task %s failed after %d attempts; giving up", task.ID, state.Attempt)
		}

	default: // Unknown
		if state.Attempt < 2 {
			// Retry once for unknown results.
			state.Status = queue.StatusWaiting
			backoff := exponentialBackoff(state.Attempt)
			resumeAt := time.Now().Add(backoff)
			state.ResumeAt = &resumeAt
			log.Printf("Task %s unknown result; retry once (attempt %d)", task.ID, state.Attempt)
		} else {
			state.Status = queue.StatusFailed
			log.Printf("Task %s unknown result after retry; marking failed", task.ID)
		}
	}

	now = time.Now().UTC()
	state.EndedAt = &now

	if err := queue.SaveState(stateDir, state); err != nil {
		log.Printf("ERROR: save post-run state for %s: %v", task.ID, err)
	}

	return ExitOK
}

// buildPromptWithContext prepends context file contents to the task prompt.
// Each context file is formatted as:
//
//	[File: <path>]
//	<contents>
func (r *Runner) buildPromptWithContext(task *queue.Task, state *queue.TaskState) (string, error) {
	if len(task.ContextFiles) == 0 {
		return r.maybeWrapResume(task.Prompt, state, task), nil
	}

	var b strings.Builder
	for _, fileRef := range task.ContextFiles {
		resolvedPath := fileRef
		// Resolve relative paths against the task working directory.
		if !filepath.IsAbs(resolvedPath) {
			resolvedPath = filepath.Join(task.WorkingDir, resolvedPath)
		}

		data, err := os.ReadFile(resolvedPath)
		if err != nil {
			if os.IsNotExist(err) {
				return "", fmt.Errorf("Context file '%s' not found in %s", fileRef, task.WorkingDir)
			}
			return "", fmt.Errorf("read context file '%s': %w", fileRef, err)
		}

		b.WriteString(fmt.Sprintf("[File: %s]\n", fileRef))
		b.Write(data)
		b.WriteString("\n\n")
	}

	b.WriteString(r.maybeWrapResume(task.Prompt, state, task))
	return b.String(), nil
}

// maybeWrapResume wraps the prompt with resume context if this is a retry
// and native resume is not being used.
func (r *Runner) maybeWrapResume(prompt string, state *queue.TaskState, task *queue.Task) string {
	if state.Attempt <= 1 {
		return prompt
	}

	// If we have a session ID and native resume is supported, the prompt
	// is sent as-is (the --resume flag handles continuation).
	if state.SessionID != "" && r.Adapter.SupportsResume() {
		strategy := resume.DetermineStrategy(true, r.Adapter.SupportsResume())
		if strategy == resume.NativeResume {
			return prompt
		}
	}

	// Use re-prompt strategy: wrap with resume context.
	return resume.BuildResumePrompt(state.Attempt, state.LastNDJSONMessages, prompt)
}

// processControlCommands reads and applies queued control commands, then
// clears the command file.
func (r *Runner) processControlCommands(controlDir, stateDir string) error {
	commands, err := queue.ReadCommands(controlDir)
	if err != nil {
		return err
	}
	if len(commands) == 0 {
		return nil
	}

	for _, cmd := range commands {
		st, err := queue.LoadState(stateDir, cmd.TaskID)
		if err != nil {
			log.Printf("WARN: control cmd %s for %s: load state: %v", cmd.Op, cmd.TaskID, err)
			continue
		}
		if st == nil {
			st = &queue.TaskState{
				ID:     cmd.TaskID,
				Status: queue.StatusPending,
			}
		}

		switch cmd.Op {
		case "retry":
			if st.Status == queue.StatusFailed || st.Status == queue.StatusCancelled {
				if queue.ValidTransition(st.Status, queue.StatusPending) {
					st.Status = queue.StatusPending
					st.Attempt = 0
					st.ResumeAt = nil
					log.Printf("Control: retrying task %s", cmd.TaskID)
				}
			}
		case "cancel":
			if queue.ValidTransition(st.Status, queue.StatusCancelled) {
				st.Status = queue.StatusCancelled
				log.Printf("Control: cancelled task %s", cmd.TaskID)
			}
		default:
			log.Printf("WARN: unknown control op %q for task %s", cmd.Op, cmd.TaskID)
			continue
		}

		if err := queue.SaveState(stateDir, st); err != nil {
			log.Printf("WARN: control cmd %s for %s: save state: %v", cmd.Op, cmd.TaskID, err)
		}
	}

	return queue.ClearCommands(controlDir)
}

// showCountdown displays a countdown timer to the next resume time.
func (r *Runner) showCountdown(resumeAt time.Time, task *queue.Task, attempt int) {
	remaining := time.Until(resumeAt).Truncate(time.Second)
	if remaining < 0 {
		remaining = 0
	}
	fmt.Printf("\r  Waiting for %s (attempt %d) — resumes in %v  ",
		task.ID, attempt, remaining)
}

// printSummary prints a completion summary of all tasks.
func (r *Runner) printSummary(stateDir string, runStarted time.Time) {
	base := config.BaseDir()
	globalTaskDir := filepath.Join(base, "tasks")

	tasks, _, err := queue.LoadTasksAndInit(globalTaskDir, r.ProjectDir, stateDir)
	if err != nil {
		log.Printf("WARN: could not load tasks for summary: %v", err)
		return
	}

	var done, failed, cancelled, pending, waiting int
	for _, t := range tasks {
		st, _ := queue.LoadState(stateDir, t.ID)
		if st == nil {
			pending++
			continue
		}
		switch st.Status {
		case queue.StatusDone:
			done++
		case queue.StatusFailed:
			failed++
		case queue.StatusCancelled:
			cancelled++
		case queue.StatusPending:
			pending++
		case queue.StatusWaiting:
			waiting++
		}

		retries := st.Attempt - 1
		if retries < 0 {
			retries = 0
		}
		duration := formatTaskDuration(st.StartedAt, st.EndedAt)
		line := fmt.Sprintf("Task %s: %s (%s, %d retries)", t.ID, strings.ToUpper(st.Status), duration, retries)
		fmt.Println(line)
		_ = appendSummaryLog(line)
	}

	fmt.Println()
	fmt.Println("=== Run Summary ===")
	fmt.Printf("  Done:      %d\n", done)
	fmt.Printf("  Failed:    %d\n", failed)
	fmt.Printf("  Cancelled: %d\n", cancelled)
	fmt.Printf("  Pending:   %d\n", pending)
	fmt.Printf("  Waiting:   %d\n", waiting)
	fmt.Printf("  Total:     %d\n", len(tasks))
	fmt.Printf("  Elapsed:   %s\n", time.Since(runStarted).Truncate(time.Second))

	_ = appendSummaryLog(fmt.Sprintf("Run completed: done=%d failed=%d cancelled=%d pending=%d waiting=%d total=%d elapsed=%s",
		done, failed, cancelled, pending, waiting, len(tasks), time.Since(runStarted).Truncate(time.Second)))
}

// checkFirstRun checks for the .first-run-ack file. If it does not exist,
// prompts the user for acknowledgement. Returns true if the user acknowledged
// (or the file already exists), false if declined.
func (r *Runner) checkFirstRun() bool {
	ackPath := filepath.Join(config.BaseDir(), ".first-run-ack")

	if _, err := os.Stat(ackPath); err == nil {
		return true
	}

	// Headless/non-interactive mode: don't block on stdin.
	if fi, err := os.Stdin.Stat(); err == nil && (fi.Mode()&os.ModeCharDevice) == 0 {
		return true
	}

	fmt.Println("=== claude-autopilot: First Run ===")
	fmt.Println()
	fmt.Println("This tool will autonomously run Claude Code on your tasks.")
	fmt.Println("It will execute commands and modify files in your working directories.")
	fmt.Println()
	fmt.Println("Please ensure you have reviewed your task definitions and understand")
	fmt.Println("that claude-autopilot will invoke 'claude' with the prompts you provide.")
	fmt.Println()
	fmt.Print("Type 'yes' to acknowledge and continue: ")

	var response string
	fmt.Scanln(&response)

	if strings.TrimSpace(strings.ToLower(response)) != "yes" {
		return false
	}

	// Create ack file.
	if err := os.WriteFile(ackPath, []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0644); err != nil {
		log.Printf("WARN: could not write first-run-ack: %v", err)
	}

	return true
}

// findEarliestResume finds the earliest resume_at time among waiting tasks.
func (r *Runner) findEarliestResume(tasks []queue.Task, states map[string]*queue.TaskState) *time.Time {
	var earliest *time.Time
	for _, t := range tasks {
		st := states[t.ID]
		if st == nil || st.ResumeAt == nil {
			continue
		}
		if earliest == nil || st.ResumeAt.Before(*earliest) {
			earliest = st.ResumeAt
		}
	}
	return earliest
}

// matchesPromptPattern checks if the recent output matches any of the
// configured prompt patterns (used for hang detection).
func (r *Runner) matchesPromptPattern(text string) bool {
	lower := strings.ToLower(text)
	for _, p := range r.promptPatterns {
		if strings.Contains(lower, strings.ToLower(p)) {
			return true
		}
	}
	return false
}

// currentGitCommit returns the current HEAD commit hash for the given
// directory, or an empty string if it cannot be determined.
func (r *Runner) currentGitCommit(dir string) string {
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// hashPrompt returns a truncated SHA-256 hash of the prompt string.
func hashPrompt(prompt string) string {
	h := sha256.Sum256([]byte(prompt))
	return fmt.Sprintf("%x", h[:8])
}

// exponentialBackoff calculates the retry delay for a given attempt number.
// Base delay is 5 minutes, doubling each attempt, capped at 300 minutes
// (5 hours). A random jitter of +/-20% is applied.
func exponentialBackoff(attempt int) time.Duration {
	const (
		baseMinutes = 5.0
		capMinutes  = 300.0
		jitterPct   = 0.20
	)

	minutes := baseMinutes * math.Pow(2, float64(attempt-1))
	if minutes > capMinutes {
		minutes = capMinutes
	}

	// Apply +/- 20% jitter.
	jitter := minutes * jitterPct * (2*rand.Float64() - 1)
	minutes += jitter

	if minutes < 1 {
		minutes = 1
	}

	return time.Duration(minutes * float64(time.Minute))
}

func rotateLogIfNeeded(path string, maxBytes int64) error {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if info.Size() <= maxBytes {
		return nil
	}

	backup := path + ".1"
	_ = os.Remove(backup)
	return os.Rename(path, backup)
}

func appendSummaryLog(line string) error {
	logDir := filepath.Join(config.BaseDir(), "logs")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return err
	}
	summaryPath := filepath.Join(logDir, "summary.log")
	f, err := os.OpenFile(summaryPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintf(f, "[%s] %s\n", time.Now().UTC().Format(time.RFC3339), line)
	return err
}

func formatTaskDuration(startedAt, endedAt *time.Time) string {
	if startedAt == nil {
		return "n/a"
	}
	end := time.Now()
	if endedAt != nil {
		end = *endedAt
	}
	if end.Before(*startedAt) {
		return "n/a"
	}
	return end.Sub(*startedAt).Truncate(time.Second).String()
}
