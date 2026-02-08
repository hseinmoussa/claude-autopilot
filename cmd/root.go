package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/hseinmoussa/claude-autopilot/internal/compat"
	"github.com/hseinmoussa/claude-autopilot/internal/config"
	"github.com/hseinmoussa/claude-autopilot/internal/detector"
	"github.com/hseinmoussa/claude-autopilot/internal/fileutil"
	"github.com/hseinmoussa/claude-autopilot/internal/lock"
	"github.com/hseinmoussa/claude-autopilot/internal/notifier"
	"github.com/hseinmoussa/claude-autopilot/internal/queue"
	"github.com/hseinmoussa/claude-autopilot/internal/runner"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// projectDir is the global --project-dir flag value.
var projectDir string

// rootCmd is the top-level cobra command for claude-autopilot.
var rootCmd = &cobra.Command{
	Use:   "claude-autopilot",
	Short: "Autonomous task runner for Claude Code",
	Long:  "Autonomous task runner for Claude Code — auto-retries on rate limits, queues tasks, keeps working while you sleep.",
}

// SetVersion sets the CLI version string shown by --version.
func SetVersion(v string) {
	rootCmd.Version = v
}

// ── add ─────────────────────────────────────────────────────────────────

var addCmd = &cobra.Command{
	Use:   "add [prompt]",
	Short: "Add a new task to the queue",
	Args:  cobra.ExactArgs(1),
	RunE:  runAdd,
}

var (
	addDir             string
	addTitle           string
	addPriority        int
	addModel           string
	addSkipPermissions bool
	addID              string
)

func runAdd(cmd *cobra.Command, args []string) error {
	prompt := args[0]

	if addDir == "" {
		return fmt.Errorf("--dir is required")
	}

	// Validate and resolve --dir to absolute path.
	absDir, err := filepath.Abs(addDir)
	if err != nil {
		return fmt.Errorf("resolve --dir: %w", err)
	}
	info, err := os.Stat(absDir)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("Directory %s does not exist", absDir)
		}
		return fmt.Errorf("--dir %q: %w", absDir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("--dir %q is not a directory", absDir)
	}

	// Ensure base directories exist.
	if err := config.EnsureDirs(); err != nil {
		return fmt.Errorf("create directories: %w", err)
	}

	// Generate title from prompt if not provided.
	title := addTitle
	if title == "" {
		title = prompt
		if len(title) > 60 {
			title = title[:60]
		}
	}

	// Generate ID if not provided.
	id := addID
	if id == "" {
		id = queue.GenerateID(title)
	} else if !queue.IsValidID(id) {
		return fmt.Errorf("id must match [a-z0-9-] and be <= 64 characters")
	}

	task := queue.Task{
		ID:              id,
		Title:           title,
		Priority:        addPriority,
		CreatedAt:       time.Now().UTC(),
		WorkingDir:      absDir,
		SkipPermissions: addSkipPermissions,
		Prompt:          prompt,
		Model:           addModel,
	}

	data, err := yaml.Marshal(&task)
	if err != nil {
		return fmt.Errorf("marshal task: %w", err)
	}

	taskPath := filepath.Join(config.BaseDir(), "tasks", id+".yaml")
	if _, err := os.Stat(taskPath); err == nil {
		return fmt.Errorf("task with id %q already exists", id)
	}
	if err := fileutil.AtomicWrite(taskPath, data, 0644); err != nil {
		return fmt.Errorf("write task file: %w", err)
	}

	fmt.Printf("Added task '%s' (priority: %d)\n", id, addPriority)
	return nil
}

// ── run ─────────────────────────────────────────────────────────────────

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Start the autonomous task runner",
	RunE:  runRun,
}

var runYes bool

func runRun(cmd *cobra.Command, args []string) error {
	// Detect Claude Code version.
	version, err := compat.DetectVersion()
	if err != nil {
		return fmt.Errorf("detect claude version: %w", err)
	}

	entry, err := compat.LookupCompat(version)
	if err != nil {
		return fmt.Errorf("lookup compat for version %s: %w", version, err)
	}

	adapter := compat.NewAdapter(entry)

	// Load matchers for detection.
	matchers, err := config.LoadMatchers()
	if err != nil {
		return fmt.Errorf("load matchers: %w", err)
	}

	det := detector.NewDetector(matchers.RateLimitPatterns, adapter.RateLimitExitCode())

	// Load configuration.
	cfg, err := config.Load(nil)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	nfy := notifier.NewNotifier(&cfg)

	r := &runner.Runner{
		Config:         &cfg,
		Adapter:        adapter,
		Detector:       det,
		Notifier:       nfy,
		ProjectDir:     resolveProjectDir(),
		YesFlag:        runYes,
		PromptPatterns: matchers.PromptPatterns,
	}

	exitCode := r.Run()
	if exitCode != 0 {
		os.Exit(exitCode)
	}
	return nil
}

// ── list ────────────────────────────────────────────────────────────────

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List all queued tasks",
	RunE:  runList,
}

func runList(cmd *cobra.Command, args []string) error {
	if err := config.EnsureDirs(); err != nil {
		return fmt.Errorf("create directories: %w", err)
	}

	base := config.BaseDir()
	globalTaskDir := filepath.Join(base, "tasks")
	stateDir := filepath.Join(base, "state")

	tasks, initCount, err := queue.LoadTasksAndInit(globalTaskDir, resolveProjectDir(), stateDir)
	if err != nil {
		return fmt.Errorf("load tasks: %w", err)
	}
	if initCount > 0 {
		fmt.Printf("ℹ Initialized state for %d new tasks\n", initCount)
	}

	if len(tasks) == 0 {
		fmt.Println("No tasks found.")
		return nil
	}

	// Ensure init files and load states.
	type taskRow struct {
		Index    int
		ID       string
		Priority int
		Status   string
		Title    string
	}

	var rows []taskRow
	for i := range tasks {
		st, err := queue.LoadState(stateDir, tasks[i].ID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: load state for %s: %v\n", tasks[i].ID, err)
		}

		status := queue.StatusPending
		if st != nil {
			status = st.Status
		}

		title := tasks[i].Title
		if len(title) > 50 {
			title = title[:50] + "..."
		}

		rows = append(rows, taskRow{
			Index:    i + 1,
			ID:       tasks[i].ID,
			Priority: tasks[i].Priority,
			Status:   status,
			Title:    title,
		})
	}

	// Print table header.
	fmt.Printf("%-4s %-30s %-8s %-12s %s\n", "#", "ID", "Priority", "Status", "Title")
	fmt.Printf("%-4s %-30s %-8s %-12s %s\n", "---", "---", "---", "---", "---")

	for _, r := range rows {
		fmt.Printf("%-4d %-30s %-8d %-12s %s\n", r.Index, r.ID, r.Priority, r.Status, r.Title)
	}

	return nil
}

// ── status ──────────────────────────────────────────────────────────────

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show runner and queue status",
	RunE:  runStatus,
}

func runStatus(cmd *cobra.Command, args []string) error {
	if err := config.EnsureDirs(); err != nil {
		return fmt.Errorf("create directories: %w", err)
	}

	base := config.BaseDir()
	lockPath := filepath.Join(base, "runner.lock")
	globalTaskDir := filepath.Join(base, "tasks")
	stateDir := filepath.Join(base, "state")

	// Probe runner lock (non-blocking).
	lk, acquired, err := lock.TryLock(lockPath)
	if err != nil {
		return fmt.Errorf("probe lock: %w", err)
	}

	if acquired {
		// We got the lock, so no runner is active. Release immediately.
		lk.Release()
		fmt.Println("Runner: idle (no active instance)")
	} else {
		// Lock is held; read lock info.
		pid, acquiredAt, err := lock.ReadLockInfo(lockPath)
		if err != nil {
			fmt.Println("Runner: active (PID unknown)")
		} else {
			fmt.Printf("Runner: active (PID %d, since %s)\n", pid, acquiredAt.Format(time.RFC3339))
		}
	}

	fmt.Println()

	// Load tasks and compute summary.
	tasks, initCount, err := queue.LoadTasksAndInit(globalTaskDir, resolveProjectDir(), stateDir)
	if err != nil {
		return fmt.Errorf("load tasks: %w", err)
	}
	if initCount > 0 {
		fmt.Printf("ℹ Initialized state for %d new tasks\n", initCount)
	}

	counts := map[string]int{
		queue.StatusPending:   0,
		queue.StatusRunning:   0,
		queue.StatusWaiting:   0,
		queue.StatusDone:      0,
		queue.StatusFailed:    0,
		queue.StatusCancelled: 0,
	}
	activeTask := ""
	var nextResume *time.Time

	for i := range tasks {
		st, _ := queue.LoadState(stateDir, tasks[i].ID)
		if st == nil {
			counts[queue.StatusPending]++
		} else {
			counts[st.Status]++
			if st.Status == queue.StatusRunning && activeTask == "" {
				activeTask = tasks[i].ID
			}
			if st.Status == queue.StatusWaiting && st.ResumeAt != nil {
				if nextResume == nil || st.ResumeAt.Before(*nextResume) {
					nextResume = st.ResumeAt
				}
			}
		}
	}

	fmt.Println("Queue:")
	fmt.Printf("  Pending:   %d\n", counts[queue.StatusPending])
	fmt.Printf("  Running:   %d\n", counts[queue.StatusRunning])
	fmt.Printf("  Waiting:   %d\n", counts[queue.StatusWaiting])
	fmt.Printf("  Done:      %d\n", counts[queue.StatusDone])
	fmt.Printf("  Failed:    %d\n", counts[queue.StatusFailed])
	fmt.Printf("  Cancelled: %d\n", counts[queue.StatusCancelled])
	fmt.Printf("  Total:     %d\n", len(tasks))
	if activeTask != "" {
		fmt.Printf("  Active:    %s\n", activeTask)
	}
	if nextResume != nil {
		fmt.Printf("  Next resume at: %s\n", nextResume.Format(time.RFC3339))
	}

	return nil
}

// ── retry ───────────────────────────────────────────────────────────────

var retryCmd = &cobra.Command{
	Use:   "retry [task-id]",
	Short: "Retry a failed or cancelled task",
	Args:  cobra.ExactArgs(1),
	RunE:  runRetry,
}

func runRetry(cmd *cobra.Command, args []string) error {
	taskID := args[0]

	if err := config.EnsureDirs(); err != nil {
		return fmt.Errorf("create directories: %w", err)
	}

	base := config.BaseDir()
	lockPath := filepath.Join(base, "runner.lock")
	stateDir := filepath.Join(base, "state")
	controlDir := filepath.Join(base, "control")

	// Try non-blocking lock acquire.
	lk, acquired, err := lock.TryLock(lockPath)
	if err != nil {
		return fmt.Errorf("probe lock: %w", err)
	}

	if acquired {
		// No runner is active; apply directly.
		defer lk.Release()

		st, err := queue.LoadState(stateDir, taskID)
		if err != nil {
			return fmt.Errorf("load state for %s: %w", taskID, err)
		}
		if st == nil {
			return fmt.Errorf("no state found for task %s", taskID)
		}

		if st.Status != queue.StatusFailed && st.Status != queue.StatusCancelled {
			return fmt.Errorf("Task '%s' is %s, only failed/cancelled tasks can be retried", taskID, st.Status)
		}

		st.Status = queue.StatusPending
		st.Attempt = 0
		st.ResumeAt = nil

		if err := queue.SaveState(stateDir, st); err != nil {
			return fmt.Errorf("save state for %s: %w", taskID, err)
		}

		fmt.Printf("Reset task '%s' to pending (attempt 0)\n", taskID)
		return nil
	}

	// Runner is active; queue the retry command.
	cc := queue.ControlCommand{
		Op:          "retry",
		TaskID:      taskID,
		RequestedAt: time.Now().UTC(),
	}
	if err := queue.AppendCommand(controlDir, cc); err != nil {
		return fmt.Errorf("queue retry command: %w", err)
	}

	fmt.Printf("Queued retry for %s\n", taskID)
	return nil
}

// ── cancel ──────────────────────────────────────────────────────────────

var cancelCmd = &cobra.Command{
	Use:   "cancel [task-id]",
	Short: "Cancel a pending, waiting, or failed task",
	Args:  cobra.ExactArgs(1),
	RunE:  runCancel,
}

func runCancel(cmd *cobra.Command, args []string) error {
	taskID := args[0]

	if err := config.EnsureDirs(); err != nil {
		return fmt.Errorf("create directories: %w", err)
	}

	base := config.BaseDir()
	lockPath := filepath.Join(base, "runner.lock")
	stateDir := filepath.Join(base, "state")
	controlDir := filepath.Join(base, "control")

	// Try non-blocking lock acquire.
	lk, acquired, err := lock.TryLock(lockPath)
	if err != nil {
		return fmt.Errorf("probe lock: %w", err)
	}

	if acquired {
		// No runner is active; apply directly.
		defer lk.Release()

		st, err := queue.LoadState(stateDir, taskID)
		if err != nil {
			return fmt.Errorf("load state for %s: %w", taskID, err)
		}
		if st == nil {
			// No state means pending; create and set to cancelled.
			st = &queue.TaskState{
				ID:     taskID,
				Status: queue.StatusPending,
			}
		}

		switch st.Status {
		case queue.StatusDone:
			fmt.Printf("Task '%s' already completed\n", taskID)
			return nil
		case queue.StatusCancelled:
			// idempotent no-op
			return nil
		case queue.StatusRunning:
			fmt.Printf("Task '%s' is currently running. It will be marked cancelled after it completes or on next queue reload.\n", taskID)
			return nil
		case queue.StatusPending, queue.StatusWaiting, queue.StatusFailed:
			if !queue.ValidTransition(st.Status, queue.StatusCancelled) {
				return fmt.Errorf("cannot transition task %s from %s to cancelled", taskID, st.Status)
			}
			st.Status = queue.StatusCancelled
			if err := queue.SaveState(stateDir, st); err != nil {
				return fmt.Errorf("save state for %s: %w", taskID, err)
			}
			fmt.Printf("Cancelled task '%s'\n", taskID)
			return nil
		default:
			return fmt.Errorf("task %s has unexpected status %q", taskID, st.Status)
		}
	}

	// Runner is active; queue the cancel command.
	cc := queue.ControlCommand{
		Op:          "cancel",
		TaskID:      taskID,
		RequestedAt: time.Now().UTC(),
	}
	if err := queue.AppendCommand(controlDir, cc); err != nil {
		return fmt.Errorf("queue cancel command: %w", err)
	}

	fmt.Printf("Queued cancel for %s\n", taskID)
	return nil
}

// ── clean ───────────────────────────────────────────────────────────────

var cleanCmd = &cobra.Command{
	Use:   "clean",
	Short: "Clean orphan temp files and rotated log backups",
	RunE:  runClean,
}

func runClean(cmd *cobra.Command, args []string) error {
	if err := config.EnsureDirs(); err != nil {
		return fmt.Errorf("create directories: %w", err)
	}

	base := config.BaseDir()
	cleanDirs := []string{
		base,
		filepath.Join(base, "state"),
		filepath.Join(base, "tasks"),
		filepath.Join(base, "control"),
	}
	if projectTasksDir := resolveProjectDir(); projectTasksDir != "" {
		cleanDirs = append(cleanDirs, projectTasksDir)
	}

	n, err := fileutil.CleanOrphanTemps(cleanDirs)
	if err != nil {
		return fmt.Errorf("clean orphan temps: %w", err)
	}

	// Clean rotated log backups (*.log.N).
	logDir := filepath.Join(base, "logs")
	rotated := 0
	entries, readErr := os.ReadDir(logDir)
	if readErr == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := entry.Name()
			// Match rotated log pattern: *.log.N
			if !strings.HasSuffix(name, ".log") && strings.Contains(name, ".log.") {
				if err := os.Remove(filepath.Join(logDir, name)); err == nil {
					rotated++
				}
			}
		}
	}

	fmt.Printf("Cleaned artifacts: %d temp files, %d log files\n", n, rotated)
	return nil
}

// ── config ──────────────────────────────────────────────────────────────

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage configuration",
}

var configSetCmd = &cobra.Command{
	Use:   "set [key] [value]",
	Short: "Set a configuration value",
	Args:  cobra.ExactArgs(2),
	RunE:  runConfigSet,
}

func runConfigSet(cmd *cobra.Command, args []string) error {
	key, value := args[0], args[1]

	if err := config.ValidateKey(key); err != nil {
		return err
	}

	if err := config.EnsureDirs(); err != nil {
		return fmt.Errorf("create directories: %w", err)
	}

	if err := config.SetConfigValue(key, value); err != nil {
		return fmt.Errorf("set config: %w", err)
	}

	fmt.Printf("Set %s = %s\n", key, value)
	return nil
}

var configGetCmd = &cobra.Command{
	Use:   "get [key]",
	Short: "Get a configuration value",
	Args:  cobra.ExactArgs(1),
	RunE:  runConfigGet,
}

func runConfigGet(cmd *cobra.Command, args []string) error {
	key := args[0]

	val, err := config.GetConfigValue(key)
	if err != nil {
		return err
	}

	// Determine source layer.
	source := resolveSource(key)
	fmt.Printf("%s = %s (source: %s)\n", key, val, source)
	return nil
}

var configListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all configuration values",
	RunE:  runConfigList,
}

func runConfigList(cmd *cobra.Command, args []string) error {
	values, err := config.ListConfig()
	if err != nil {
		return fmt.Errorf("list config: %w", err)
	}

	// Sort keys for deterministic output.
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		source := resolveSource(k)
		fmt.Printf("%-25s = %-20s (source: %s)\n", k, values[k], source)
	}

	return nil
}

var configPathCmd = &cobra.Command{
	Use:   "path",
	Short: "Print the config file path",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println(filepath.Join(config.BaseDir(), "config.yaml"))
	},
}

// resolveSource determines which configuration layer provided the effective
// value for a key: env, file, or default.
func resolveSource(key string) string {
	envKey := "CLAUDE_AUTOPILOT_" + strings.ToUpper(key)
	if _, ok := os.LookupEnv(envKey); ok {
		return "env"
	}

	// Check if the key is set in the config file by attempting to read raw.
	data, err := os.ReadFile(filepath.Join(config.BaseDir(), "config.yaml"))
	if err == nil && len(data) > 0 {
		var raw map[string]interface{}
		if yaml.Unmarshal(data, &raw) == nil {
			if _, ok := raw[key]; ok {
				return "file"
			}
		}
	}

	return "default"
}

// ── helpers ─────────────────────────────────────────────────────────────

// resolveProjectDir returns the effective project-local task directory.
// If --project-dir was given, it is resolved to an absolute path. Otherwise
// the current working directory is used.
func resolveProjectDir() string {
	if projectDir != "" {
		abs, err := filepath.Abs(projectDir)
		if err != nil {
			return projectDir
		}
		return filepath.Join(abs, ".autopilot", "tasks")
	}

	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return filepath.Join(cwd, ".autopilot", "tasks")
}

// ── init & execute ──────────────────────────────────────────────────────

func init() {
	// Global flags.
	rootCmd.PersistentFlags().StringVar(&projectDir, "project-dir", "", "project-local task directory (default: cwd)")

	// add command flags.
	addCmd.Flags().StringVar(&addDir, "dir", "", "working directory for the task (required)")
	addCmd.Flags().StringVar(&addTitle, "title", "", "task title (default: first 60 chars of prompt)")
	addCmd.Flags().IntVar(&addPriority, "priority", 10, "task priority (lower = higher priority)")
	addCmd.Flags().StringVar(&addModel, "model", "", "Claude model to use")
	addCmd.Flags().BoolVar(&addSkipPermissions, "skip-permissions", false, "skip permission prompts")
	addCmd.Flags().StringVar(&addID, "id", "", "task ID (default: auto-generated)")
	_ = addCmd.MarkFlagRequired("dir")

	// run command flags.
	runCmd.Flags().BoolVarP(&runYes, "yes", "y", false, "skip first-run safety prompt")

	// config subcommands.
	configCmd.AddCommand(configSetCmd)
	configCmd.AddCommand(configGetCmd)
	configCmd.AddCommand(configListCmd)
	configCmd.AddCommand(configPathCmd)

	// Register all commands on root.
	rootCmd.AddCommand(addCmd)
	rootCmd.AddCommand(runCmd)
	rootCmd.AddCommand(listCmd)
	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(retryCmd)
	rootCmd.AddCommand(cancelCmd)
	rootCmd.AddCommand(cleanCmd)
	rootCmd.AddCommand(configCmd)
}

// Execute runs the root command and returns any error. The caller (main.go)
// is responsible for calling os.Exit on error.
func Execute() error {
	if err := rootCmd.Execute(); err != nil {
		// cobra already printed the error; check if it's a known sentinel.
		var exitErr *exitError
		if errors.As(err, &exitErr) {
			os.Exit(exitErr.code)
		}
		return err
	}
	return nil
}

// exitError wraps an exit code for signaling from RunE handlers.
type exitError struct {
	code int
}

func (e *exitError) Error() string {
	return fmt.Sprintf("exit code %d", e.code)
}
