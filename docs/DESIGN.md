# claude-autopilot — Autonomous Claude Code Task Runner

A Go CLI that queues coding tasks for Claude Code, auto-retries on rate limits, and keeps working while you sleep. Walk away, come back to finished work.

---

## The Problem

You're working on a project with Claude Code (or Conductor), hit the rate limit, and have to wait hours. You want to go outside / sleep and have it **automatically resume and keep going** when the limit resets.

## How It Works

```
┌─────────────┐     ┌────────────────┐     ┌──────────────┐
│  Task Queue  │────▶│ claude-autopilot│────▶│  Claude Code  │
│ (tasks.yaml) │     │  (Go binary)   │     │  (subprocess) │
└─────────────┘     └───────┬────────┘     └──────────────┘
                            │
                     Rate limit hit?
                            │
                     ┌──────▼───────┐
                     │ Wait + Retry  │
                     │ (auto-resume) │
                     └──────────────┘
```

1. You define tasks in a queue (YAML files or CLI commands)
2. `claude-autopilot` spawns Claude Code as a subprocess for each task
3. If rate-limited → parses the reset time → sleeps → auto-resumes
4. Logs everything so you can review what happened while you were away
5. Sends you a notification (terminal bell, webhook, etc.) when done

---

## Phase 1: Project Setup

- [x] Initialize Go module (`go mod init github.com/hseinmoussa/claude-autopilot`)
- [x] Project structure:
  ```
  claude-autopilot/
  ├── cmd/
  │   └── root.go         # Cobra CLI commands (add, run, list, status, retry, cancel, clean, config)
  ├── internal/
  │   ├── queue/           # Task queue CRUD + YAML parsing + ordering
  │   ├── runner/          # Claude Code subprocess runner + graceful shutdown
  │   ├── compat/          # CLI version detection + adapter interface
  │   ├── detector/        # Rate limit detection (layered)
  │   ├── resume/          # Resume logic (native --resume vs re-prompt fallback)
  │   ├── timeparse/       # Timezone-aware reset time parser
  │   ├── lock/            # flock-based process locking
  │   ├── fileutil/        # Atomic write + fsync helpers
  │   ├── notifier/        # Notifications (bell, webhook, desktop)
  │   └── config/          # Config loading + matchers
  │       └── matchers.default.yaml  # Default rate limit patterns (embedded via go:embed)
  ├── test/
  │   ├── mock_claude.sh   # Fake claude binary for integration tests (POSIX only)
  │   └── smoke.sh         # End-to-end smoke test (POSIX only; CI runs on Linux)
  ├── main.go
  ├── tasks.yaml.example     # Example task queue file
  ├── README.md
  ├── Makefile
  └── go.mod
  ```
- [x] Add `.gitignore` (binaries, `.env`, logs)
- [x] **Directory auto-creation**: all commands that write files must create their target directories via `os.MkdirAll` on first use. Required directories: `~/.claude-autopilot/`, `~/.claude-autopilot/state/`, `~/.claude-autopilot/tasks/`, `~/.claude-autopilot/logs/`, and `~/.claude-autopilot/control/`. No manual setup step required — first invocation of any command bootstraps the directory structure. (Project-local `.autopilot/tasks/` directories are user-managed in MVP; `add` writes to global tasks only.)
- [x] Temp files are named `<filename>.tmp.<pid>.<random>` — PID-scoped to identify ownership
- [x] On startup: sweep `*.tmp.*` in **all directories where atomic writes occur** (`~/.claude-autopilot/` root, `~/.claude-autopilot/state/`, `~/.claude-autopilot/tasks/`, `~/.claude-autopilot/control/`, and any configured project-local task dirs). Two-pass cleanup:
  1. **Pass 1 — dead-owner cleanup**: for each temp file, extract embedded PID from filename. Check liveness:
     - Linux/macOS: `kill(pid, 0)` syscall (returns ESRCH if dead)
     - Windows: `OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION, pid)` — fails with ERROR_INVALID_PARAMETER if PID doesn't exist
     - If PID is dead → delete (orphan from crashed process)
     - If PID is alive → skip (owner may still be writing)
     - If liveness check fails (unexpected error) → skip (leave for next sweep)
  2. **Pass 2 — age-based fallback**: delete any remaining `*.tmp.*` with **mtime > 24 hours**, even if the embedded PID is alive. This handles PID reuse (a new process inherited the dead writer's PID, making pass 1 think the file is still owned). A 24h-old temp file is never legitimate — atomic writes complete in milliseconds.
  - Pass 2 is strictly a safety net for the PID-reuse edge case in pass 1. Both passes are needed.

## Phase 2: Task Queue

### Task Format: YAML files (not markdown)

Each task is a standalone `.yaml` file in `~/.claude-autopilot/tasks/` or a project-local `.autopilot/tasks/` dir. YAML avoids the multiline/escaping ambiguity of markdown parsing.

- [x] Define strict task schema:
  ```yaml
  # ~/.claude-autopilot/tasks/001-auth-module.yaml
  id: auth-module                    # unique; auto-generated as slugified-title-<4hex> if omitted (e.g. "setup-auth-module-a3f1"). Must be filesystem-safe: [a-z0-9-], max 64 chars.
  title: "Setup auth module"
  priority: 1                        # lower = runs first (default: 10 if omitted)
  created_at: 2025-02-08T14:00:00Z  # auto-set by CLI, optional in hand-written files
  working_dir: /path/to/project      # validated on `add` (must exist) and on `run` (fail task if missing). Must be absolute path; `run` rejects relative paths with: "Task '<id>': working_dir must be absolute (got '<path>'). Use 'add --dir' which resolves automatically."
  skip_permissions: true             # optional: enable --dangerously-skip-permissions
  prompt: |
    Create a JWT authentication module with login/signup endpoints.
    Use Go + chi router. Write tests.
  context_files:                     # optional: files to feed as context (see context_files handling below)
    - src/routes.go
    - docs/auth-spec.md
  model: claude-sonnet-4-5-20250929  # optional: override default
  max_retries: 5                     # optional: default 5
  estimated_tokens: 2000             # optional: reserved for future cost tracking (not used in MVP)
  flags:                             # optional: extra claude CLI flags
    - "--allowedTools"
    - "Edit,Bash"
  ```
- [x] Also support a single multi-task file (`tasks.yaml`) with `---` document separators
- [x] **Task source loading order** (when both global and project-local exist):
  1. Global: `~/.claude-autopilot/tasks/*.yaml` + `~/.claude-autopilot/tasks.yaml`
  2. Project-local: `.autopilot/tasks/*.yaml` + `.autopilot/tasks.yaml`
  3. All sources are merged into one queue. Duplicate IDs across sources → error (see duplicate detection)
  4. Project-local dir is determined by `--project-dir` flag or `cwd`. Only one project-local dir per invocation.
  5. `--project-dir` is a **global flag** available on all commands that load tasks (`run`, `list`, `status`, `clean`, `retry`, `cancel`). Without it, `cwd` is used as the project-local root. This is distinct from `add --dir` which sets the task's `working_dir`.
- [x] **`context_files` handling**: paths are resolved relative to the task's `working_dir`:
  - On `run`, before spawning Claude Code, read each file and prepend contents to prompt:
    ```
    [File: src/routes.go]
    <file contents>
    
    [File: docs/auth-spec.md]
    <file contents>
    
    <original prompt>
    ```
  - If a context file doesn't exist → **fail the task** with clear error: `"Context file 'src/routes.go' not found in <working_dir>"`
  - This is validated at run-time (not add-time) because files may not exist yet when the task is queued
- [x] Validate schema on load (fail fast with clear errors for missing/invalid fields):
  - **Required fields**: `prompt` (non-empty string), `working_dir` (valid absolute path)
  - **Auto-generated if absent**: `id` (slugified title + 4 hex), `title` (first 60 chars of prompt), `created_at` (via `.init.json`)
  - **Optional with defaults**: `priority` (default: 10), `max_retries` (default: 5), `skip_permissions` (default: false)
  - **Optional, no default**: `model`, `context_files`, `flags`, `estimated_tokens`
  - **Invalid field error format**: `"Task '<id>' (<source>): missing required field 'prompt'"`

### Queue Ordering: Deterministic Execution Order

Tasks come from two sources (per-task `.yaml` files AND multi-doc `tasks.yaml`).
Ordering must be deterministic and predictable.

- [x] **Merge strategy**: on `run`, load all tasks from both sources into a single in-memory queue
- [x] **Sort key**: `(priority ASC, created_at ASC, id ASC)`
  - Lower priority number runs first (0 = highest)
  - Ties in priority → earlier `created_at` wins (FIFO)
  - Ties in both → alphabetical by `id` (deterministic tiebreaker)
- [x] `created_at` is auto-set when task is added via CLI (written into the YAML file)
- [x] For hand-written YAML files: `created_at` is an **optional field** — if omitted:
  - On first load (by ANY command: `run`, `list`, `status`), initialize `created_at` via the `.init.json` file (see below)
  - All subsequent loads read `created_at` from `.init.json`, never recompute it
  - **Side effect disclosure**: `list`/`status` can create `.init.json` files on first encounter with new tasks. This is intentional:
    - Print one-time notice: `"ℹ Initialized state for N new tasks"`
    - Subsequent calls are purely read-only
    - Rejected alternative: lazy init only on `run` — would cause `list` to show non-deterministic ordering for new tasks
  - **Canonical storage**: `created_at` lives **only** in `<task-id>.init.json` — never in `.state.json`, never recomputed
  - **First-write atomicity**: `.init.json` uses a **two-step exclusive create**:
    1. Atomic-write to a temp file (`<task-id>.init.json.tmp.<pid>.<random>`) + `fsync(file)`
    2. `link(tmp, target)` — fails with `EEXIST` if target already exists, providing race safety without risk of partial files at the target path
    3. `fsync(parent_dir)` — ensures the new directory entry (hardlink) is durable on disk after power loss
    4. `unlink(tmp)` — clean up temp regardless of link success/failure
    5. If `link` fails with `EEXIST` → another process won → read existing file
    - This combines crash safety (never a partial file at the real path) with race safety (exactly one winner)
    - **Platform support**: `link()` works on Linux, macOS, and Windows (NTFS). If `link()` fails with `ENOTSUP` or `EPERM` (rare: FAT32, some network mounts), **error with actionable message**: `"Init file creation failed: filesystem does not support hardlinks. Move ~/.claude-autopilot/state to a local filesystem (ext4, APFS, NTFS)."` This preserves the crash-safety guarantee with no exceptions.
    - If crash happens after step 1 but before step 2 → only orphan temp file remains, cleaned up on next startup via stale temp sweep
- [x] Editing a task file does NOT change its queue position (mtime is never used for ordering)
- [x] **Duplicate detection**: error on load if the same `id` appears more than once from **any combination of sources**:
  - Two standalone YAML files with the same `id`
  - Two documents within the same `tasks.yaml` (multi-doc YAML) with the same `id`
  - A standalone file and a `tasks.yaml` entry with the same `id`
  - Error format: `"Duplicate task ID '<id>' found in <source1> and <source2>. Remove one."`
- [x] **Validation on load**: all tasks validated + sorted before any execution starts (fail-fast)
- [x] `claude-autopilot list` shows tasks in exact execution order with their resolved priority
- [x] Task state tracking in two files per task under `~/.claude-autopilot/state/`:
  - **`<task-id>.init.json`** — immutable after creation, written once via temp+hardlink (see Phase 2 init.json section) by whichever command first encounters the task. Contains only `created_at`:
    ```json
    {"id": "auth-module", "created_at": "2025-02-08T14:00:00Z"}
    ```
  - **`<task-id>.state.json`** — mutable state for each task. Mutations happen only via state-mutating commands (`run`, `retry`, `cancel`) and are serialized under the runner lock (either directly or via command queue processed by `run`). Does NOT contain `created_at`. This is the **single mutable state file** per task — there is no separate checkpoint file. All checkpoint data lives here:
    ```json
    {
      "id": "auth-module",
      "status": "waiting",
      "attempt": 2,
      "started_at": "2025-02-08T14:00:00Z",
      "last_rate_limited_at": "2025-02-08T14:23:00Z",
      "resume_at": "2025-02-08T16:30:00Z",
      "prompt_hash": "sha256:abc123...",
      "git_commit": "def456...",
      "session_id": "550e8400-...",
      "last_ndjson_messages": ["...last 20 lines before interruption"]
    }
    ```
  - On load: merge `.init.json` + `.state.json` into a single in-memory task struct
- [x] Support adding tasks via CLI: `claude-autopilot add "fix the auth bug" --dir ./myproject --priority 1`
  - `add` writes the task YAML file to the **global** task directory (`~/.claude-autopilot/tasks/<id>.yaml`). For project-local tasks, create YAML files manually in `.autopilot/tasks/`.
  - `add` validates `working_dir` exists at add-time (fail with `"Directory <path> does not exist"`)
  - `add` resolves `working_dir` to an **absolute path** before writing to YAML (prevents cwd-dependent behavior when `run` executes from a different directory)
  - `add` generates ID if omitted: slugify title + 4 random hex chars (e.g. `setup-auth-module-a3f1`)
  - Required fields for `add`: prompt text (positional arg) and `--dir` (working directory). `title` defaults to first 60 chars of prompt if not provided via `--title`.

### Task State Machine

- [x] **States**: `pending`, `running`, `waiting`, `done`, `failed`, `cancelled`
- [x] **Initial state**: tasks start as `pending` (set on creation by `add` or first load)
- [x] **Transitions**:
  - `pending → running`: picked up by `run`
  - `running → done`: `type: "result"` received with exit code 0
  - `running → failed`: non-recoverable error, or max retries exceeded
  - `running → waiting`: rate limit detected, `resume_at` set
  - `waiting → running`: resume time reached, retry begins
  - `failed → pending`: via `retry` command (resets attempt counter)
  - `pending → cancelled`: via `cancel` command
  - `waiting → cancelled`: via `cancel` command
  - `failed → cancelled`: via `cancel` command
  - `cancelled → pending`: via `retry` command
- [x] **On crash recovery** (process killed mid-task):
  - If state is `running` on next startup → revert to `pending` for re-execution
  - If `session_id` exists in state → attempt `--resume` on next run
  - If state is `waiting` with `resume_at` in the future → respect the wait time, don't restart immediately
- [x] **`done` tasks cannot be retried** — use `retry` only on `failed`/`cancelled`
- [x] `run` **skips** tasks with status `done`, `failed`, `cancelled`, or `running` (stale `running` handled by crash recovery above; `failed` tasks must be explicitly retried via `retry` command)

### Queue Reload & Execution Loop

- [x] `run` uses a **per-task reload** strategy:
  1. Load all tasks → sort → filter to actionable (`pending`, `waiting` with elapsed `resume_at`)
  2. Pick the highest-priority actionable task → execute it
  3. After task completes (or fails/rate-limits) → **reload from disk** before picking next task
  4. If no actionable tasks exist but there are `waiting` tasks with future `resume_at`, enter a wait loop:
     - Sleep in short ticks: `tick = min(30s, nearest_resume_at - now)` (with countdown display)
     - On each tick, reload queue from disk and process any queued control commands (retry/cancel)
     - If new actionable task appears (e.g., user added a pending task) → break wait loop immediately and execute
  5. This means `add` during execution is picked up quickly (max ~30s while waiting, immediate between tasks otherwise)
  6. Loop terminates only when there are no actionable tasks **and no waiting tasks** → print summary → exit
  7. If the queue is completely empty (no tasks found from any source), print: `"No tasks found. Add tasks with 'claude-autopilot add' or create YAML files in ~/.claude-autopilot/tasks/"` and exit with code 0.
- [x] `run` is not a persistent background service — it exits when all work is complete (all tasks `done`/`failed`/`cancelled` and no `waiting` tasks). For continuous "always-on" operation, use cron or a wrapper script.
- [x] **`run` exit codes**:
  - `0`: all tasks completed as `done` (or queue was empty)
  - `1`: one or more tasks ended as `failed` (partial success)
  - `2`: lock contention (another instance running) or fatal startup error
  - `130`: interrupted by SIGINT/SIGTERM (graceful shutdown)

### CLI Commands: `retry`, `cancel`, `clean`, `config`

- [x] **`claude-autopilot retry <task-id>`**:
  - If runner lock is free: acquires lock and mutates state immediately
  - If runner lock is held by active `run`: enqueue control command in `~/.claude-autopilot/control/commands.jsonl` and return success (`"Queued retry for <task-id>"`)
  - Validation semantics:
    - Immediate mode (lock acquired now): only `failed`/`cancelled` are accepted; all other states return error (`"Task '<id>' is <status>, only failed/cancelled tasks can be retried"`)
    - Queued mode (lock busy): command is accepted into queue; runner validates at apply-time. If state is incompatible then, command is dropped with info log (not fatal).
  - Resets `attempt` to 0, sets status to `pending` (runner increments attempt to 1 before the next execution — see Phase 4b pre-run state)
  - Preserves `session_id` if present (allows `--resume` on next run)
  - Does NOT clear `last_ndjson_messages` (useful context for re-prompt fallback)
- [x] **`claude-autopilot cancel <task-id>`**:
  - If runner lock is free: acquires lock and mutates state immediately
  - If runner lock is held by active `run`: enqueue control command in `~/.claude-autopilot/control/commands.jsonl` and return success (`"Queued cancel for <task-id>"`)
  - **Accepted states**: `pending`, `waiting`, `failed` → sets to `cancelled`
  - **No-op states**: `done` (print `"Task '<id>' already completed"`), `cancelled` (idempotent, no message)
  - **Running state**: does NOT kill the subprocess. Print: `"Task '<id>' is currently running. It will be marked cancelled after it completes or on next queue reload."` The runner will pick up the cancel command and apply it to whatever state the task reaches after execution finishes (e.g. if it finishes as `failed`, the queued cancel transitions `failed → cancelled`).
- [x] **`claude-autopilot clean`**:
  - Does not mutate task state files by default; safe to run while `run` is active
  - Cleans non-authoritative artifacts: orphan `*.tmp.*` files and rotated log backups (`.log.1` files — historical data from log rotation)
  - Does NOT delete task YAML files (those are user-owned)
  - Prints: `"Cleaned artifacts: <tmp_count> temp files, <log_count> log files"`
  - Never deletes `.init.json` or `.state.json` (preserves deterministic ordering + completion state)

- [x] **`claude-autopilot config <subcommand>`**:
  - `config set <key> <value>`: write a key to `~/.claude-autopilot/config.yaml` (creates file if absent). Validates key name against known config keys; unknown keys are rejected with error.
  - `config get <key>`: print the resolved value for a key (showing which layer it came from: default, config file, env var, or CLI flag)
  - `config list`: print all config keys with their current values and source layer
  - `config path`: print the config file path (`~/.claude-autopilot/config.yaml`)
  - Config file is written via atomic temp+rename (same as other data files)
  - Known config keys for MVP: `skip_permissions`, `hang_timeout`, `webhook_url`, `notification_desktop`, `notification_bell`

- [x] Persist state to disk (survive crashes/restarts)
- [x] `claude-autopilot list` shows all tasks in deterministic execution order with resolved priority and current status
- [x] `claude-autopilot add` does NOT acquire the runner lock (only writes to task dir)

### File Write Safety: Atomic Writes + Fsync

All **data file** writes (task YAML, state JSON, init JSON) must be crash-safe.
A power loss or `kill -9` mid-write must never leave corrupted data files. The lockfile is the sole exception: it is written in-place after flock acquisition (see Process Lock section), and its content (`pid`, `acquired_at`) is ephemeral metadata that is re-written on every `run` — a partial lockfile is harmless since flock itself is the source of truth, not the file content.

- [x] **Atomic write helper** (`internal/fileutil/atomic.go`) — two strategies:
  1. **Mutable files** (`.state.json`): temp + rename
     - Write to temp file in same directory (`<filename>.tmp.<pid>.<random>`)
     - `fsync(file)`
     - `os.Rename(tmp, target)` — atomic on POSIX; on Windows, `os.Rename` calls `MoveFileEx` with `MOVEFILE_REPLACE_EXISTING` which is atomic on NTFS for same-volume renames
     - `fsync(parent_dir)` — ensures directory entry durability (Linux/macOS; no-op on Windows where NTFS journals metadata)
  2. **Create-once files** (`.init.json`): temp + hardlink
     - Write to temp file (`<filename>.tmp.<pid>.<random>`) + `fsync(file)`
     - `link(tmp, target)` — fails with `EEXIST` if target exists (race-safe)
     - `fsync(parent_dir)` — ensures hardlink durability
     - `unlink(tmp)` — cleanup regardless of link result
     - See Phase 2 init.json section for details
- [x] **Exception: lockfile** — written in-place (open → flock → truncate → write → fsync). NOT temp+rename — see Process Lock section for why.
- [x] All other file writes go through the atomic helpers — no direct `os.WriteFile` anywhere
- [x] `claude-autopilot add` is safe to call concurrently with `run` because:
  - `add` writes a new file with a unique name (slugified-title + random hex) → no conflict with existing files
  - `run` loads tasks at the start of each queue cycle, so new tasks are picked up on next iteration
  - No shared mutable file between `add` and `run`
- [x] Crash safety for state files is handled entirely by the atomic temp+rename helper (write temp → fsync → rename). No separate write-ahead pattern needed — rename is atomic on both POSIX (`rename(2)`) and Windows NTFS (`MoveFileEx` with `MOVEFILE_REPLACE_EXISTING`), so the file is either the old version or the new version, never partial.

### Process Lock: Single-Instance Enforcement

Concurrent state mutation against the same queue can corrupt state.
Use **flock** as the single source of truth (MVP requirement).

- [x] On `run`:
  1. `Open` the lockfile `~/.claude-autopilot/runner.lock` with `O_CREATE|O_RDWR`
  2. Attempt `flock(fd, LOCK_EX|LOCK_NB)` (non-blocking exclusive lock)
  3. If lock acquired → truncate file → write `{"pid": <pid>, "acquired_at": "<timestamp>"}` → `fsync` → proceed
  4. If `EWOULDBLOCK` → another process holds the lock → attempt to read file for PID:
     - If file contains valid JSON with `pid` → exit with: `"Another claude-autopilot is already running (PID: <pid>). Use 'status' to check progress."`
     - If file is empty or unparseable (race: winner acquired lock but hasn't written metadata yet) → retry read after 500ms (one retry). If still unreadable → exit with: `"Another claude-autopilot is already running. Use 'status' to check progress."` (omit PID)
- [x] On `retry` / `cancel`:
  - Try non-blocking lock acquire.
  - If lock acquired: apply mutation immediately and release lock.
  - If lock busy: append command to `~/.claude-autopilot/control/commands.jsonl` (atomic append + fsync), then exit success.
  - **Append safety**: acquire a short-lived per-file lock on `commands.jsonl` for every append (`flock` on POSIX, `LockFileEx` on Windows), then open with `O_APPEND|O_WRONLY|O_CREATE`, write one JSON line + `\n` in a single write call, `fsync`, and release the file lock. This avoids filesystem-specific append atomicity assumptions.
  - **Truncated line handling**: if a crash occurs mid-append, the file may contain a partial JSON line at the end. The runner's command processor must attempt to parse each line individually; malformed lines are logged as warnings and skipped (not fatal).
  - Command format:
    ```json
    {"op":"retry","task_id":"auth-module","requested_at":"2026-02-08T15:30:00Z"}
    ```
- [x] `run` processes queued control commands:
  - **At startup** (after acquiring lock and reloading state — picks up commands queued while runner was not active)
  - After each task completion
  - On each wait-loop tick while sleeping for future `resume_at`
  - Commands are idempotent and applied under the held runner lock
  - State-mismatch handling: if a queued command targets a task in an incompatible state (e.g. queued cancel for a task that completed as `done`), the command is dropped with an info log message — not an error
  - After successful apply pass, rewrite queue atomically to keep only unapplied/invalid lines (or truncate to empty if all applied). This prevents unbounded `commands.jsonl` growth.
- [x] `run` **must reload state from disk after acquiring the runner lock** at startup, not rely on any cached state. This ensures it sees mutations made by `retry`/`cancel` that acquired and released the lock before `run` started.
- [x] **Why flock is sufficient**: `flock` is on the open file descriptor, not the filename. When a process dies (SIGKILL, OOM, power loss), the kernel closes all FDs, which releases the lock automatically. No stale lock recovery, no PID liveness checks, no start_time comparison needed.
- [x] **Critical**: lockfile is opened + locked + written in-place. NEVER use temp+rename for the lockfile — renaming replaces the inode, which would silently bypass the flock held by another process on the old inode.
- [x] **Graceful shutdown** (SIGTERM/SIGINT on POSIX; Ctrl+C / `GenerateConsoleCtrlEvent` on Windows):
  1. Set a `shutting_down` flag → no new tasks will be picked from queue
  2. **POSIX**: send SIGTERM to the active Claude Code subprocess (if any)
     **Windows**: send `CTRL_BREAK_EVENT` via `GenerateConsoleCtrlEvent` (Claude Code's Node.js runtime handles this for clean exit). Note: Claude Code subprocess must be created in a new process group (`CREATE_NEW_PROCESS_GROUP` via Go's `SysProcAttr`) so `CTRL_BREAK_EVENT` targets only the subprocess, not claude-autopilot itself.
  3. Wait up to 10 seconds for subprocess to exit cleanly (continue reading and processing its stdout/stderr during this wait — the subprocess may emit `type: "result"` or rate limit messages before exiting)
  4. If subprocess still alive after 10s → **POSIX**: SIGKILL / **Windows**: `TerminateProcess`
  5. Save current state to `.state.json` with status-aware rules (state may have been updated by output processing in step 3):
     - If active task status is `running` (in-flight work interrupted) → set to `pending` for safe re-execution on next start
     - If task is already `waiting` with `resume_at` set (rate-limit wait established) → keep `waiting` and preserve `resume_at`
     - Never force `waiting` back to `running` during shutdown
  6. Release flock via `fd.Close()`
  7. Exit with code 130 (standard SIGINT exit code)
- [x] Release lock on clean exit via `defer fd.Close()`
- [x] `claude-autopilot status` does NOT acquire the runner lock — it only reads runner state. However, like `list`, it may create `.init.json` files for newly-discovered tasks (one-time metadata initialization, not runner state mutation). This is a deliberate design choice for deterministic ordering (see Phase 2).
  - **Lockfile read safety**: `status` reads `runner.lock` for PID info, but the lockfile is written in-place (truncate + write). A concurrent read during write may see partial/empty JSON. `status` must handle this gracefully: if lockfile is unparseable, show `"Runner: active (PID unknown)"` based on flock probe (try non-blocking lock; if EWOULDBLOCK → runner is active).
- [x] Cross-platform: use `flock` on Linux/macOS. On Windows, use `LockFileEx` via `golang.org/x/sys/windows` (same FD-based semantics, same auto-release on process death)

## Phase 3: Claude Code Subprocess Runner

### Validated approach: Use Claude Agent SDK protocol

Based on analysis of [claude-code-queue](https://github.com/JCSnap/claude-code-queue) and the
[Claude Agent SDK Spec](https://gist.github.com/SamSaffron/603648958a8c18ceae34939a8951d417),
the correct way to spawn Claude Code programmatically is:

```bash
claude --print --output-format stream-json --verbose -- "your prompt here"
```

This outputs newline-delimited JSON (NDJSON) messages to stdout with typed message objects:
- `type: "system"` → init message with session_id, model, tools
- `type: "assistant"` → Claude's response chunks
- `type: "user"` → tool use requests (human-turn inputs fed back to the model)
- `type: "result"` → final result (query complete)

### CLI Compatibility & Version Gating

The NDJSON contract, exit codes, and flag names **can change across Claude Code versions**.
We must not assume a fixed contract.

- [x] **On `run` only** (not `add`/`list`/`status`), run `claude --version` and parse the semver. This avoids failing queue-management commands when Claude Code isn't installed.
- [x] Maintain a `compat.go` file mapping version **ranges** to known behaviors:
  ```go
  // compat.go
  var compatTable = []CompatEntry{
      {MinVersion: "2.0.0", MaxVersion: "2.1.3", StreamJSON: true, ResumeFlag: true, ExitCodeRateLimit: 75},
      {MinVersion: "1.0.0", MaxVersion: "1.99.99", StreamJSON: false, ResumeFlag: false, ExitCodeRateLimit: -1},
  }
  ```
  - Version `1.x`: no `stream-json`, no `--resume`, text mode + string matching only (matches "old Claude Code" acceptance criteria)
  - Version `2.x`: full structured output + native resume
  - Version lookup: find the entry where `MinVersion <= detected <= MaxVersion`. If **no entry matches** (version is newer than all MaxVersion values, or gaps between ranges), that's the "unknown/newer" case → safe mode.
  - This eliminates the need for a separate `MaxTestedVersion` constant — the highest `MaxVersion` in the table implicitly defines the tested ceiling.
- [x] If Claude Code version **matches no range**:
  - Log a warning: `"Claude Code vX.Y.Z is newer than tested. Falling back to safe mode."`
  - **Output format fallback**: try `stream-json` first (likely supported); if it fails → `json` → `text`
  - **Resume fallback**: try `--resume` first; if it fails → re-prompt strategy
  - Output format and resume are **independent fallbacks** — a version that drops `stream-json` but keeps `--resume` should still use native resume with text output parsing
- [x] If Claude Code **is not installed** → fail with clear install instructions
- [x] If `--output-format stream-json` fails at runtime (older version?) → catch error, retry with `--output-format json` (single response), then fall back to text. This is independent of compat table — handles unexpected failures.
- [x] All version-dependent behavior goes through a `CLIAdapter` interface so it's easy to update

**Critical flags for our use case:**
- `--print` / `-p`: non-interactive mode (required for unattended execution)
- `--output-format stream-json`: structured output we can parse
- `--verbose`: required for stream-json
- `--resume <session-id>`: resume a specific session by ID (version-gated)
- `--continue`: resume the most recent session (NOT used by claude-autopilot — we always track explicit session IDs via `--resume` for deterministic behavior)
- `--max-turns <n>`: limit conversation turns
- `--model <model>`: model override

- [x] Spawn `claude` CLI as child process using the SDK protocol above
- [x] Parse NDJSON stdout line-by-line into typed Go structs
- [x] Extract `session_id` from the `system` init message (needed for `--resume`)
- [x] Persist `session_id` per task in `.state.json` (enables native resume)
- [x] Stream parsed messages to log file + optional terminal output
- [x] Detect `type: "result"` message → task complete
- [x] Detect process exit code (0 = success, non-zero = check for rate limit)
- [x] Pass through user-defined flags from task YAML (`flags` field)
- [x] Support `context_files` by prepending file contents to prompt (see Phase 2 context_files handling for format)

### Permission Safety Policy: `--dangerously-skip-permissions`

Unattended execution **requires** bypassing Claude Code's interactive permission prompts
(otherwise the process hangs waiting for user input). But this is a sharp tool.

**Policy: Global default OFF, per-task opt-in.**

- [x] Global config (`~/.claude-autopilot/config.yaml`):
  ```yaml
  # Default: false. Claude Code will hang on permission prompts in unattended mode.
  # Enable globally only if you trust ALL queued prompts.
  skip_permissions: false
  
  # Kill task if no output for this duration (catches hung processes).
  # Only applies when skip_permissions is false. Default: 10m.
  # Permission prompt patterns are detected and killed after 30s silence confirmation gate.
  hang_timeout: 10m
  ```
- [x] **Config resolution order**:
  - **Task-scoped keys** (e.g. `skip_permissions`, task model override): `CLI flag > task YAML > environment variable > global config > default`
  - **Global-only keys** (e.g. webhook URL, default hang timeout): `CLI flag > environment variable > global config > default`
  - This preserves true per-task override semantics while still allowing global env/config defaults.
  - **Config loading**: `run` loads config once at startup and caches it for the entire execution. Config changes via `config set` during an active `run` take effect on the next `run` invocation, not mid-run. This ensures consistent behavior within a single execution.
  - Env var naming: `CLAUDE_AUTOPILOT_<UPPER_SNAKE_KEY>` (handled by viper)
- [x] **Matchers merge strategy**: rate limit patterns and prompt-wait patterns are loaded from:
  1. `matchers.default.yaml` (shipped with binary, embedded via `go:embed` from `internal/config/matchers.default.yaml`)
  2. `~/.claude-autopilot/matchers.yaml` (user overrides, optional)
  3. Merge: user file **extends** defaults (user patterns are appended). To remove a default pattern, user file supports per-category exclusion lists:
     ```yaml
     # ~/.claude-autopilot/matchers.yaml
     rate_limit_patterns:
       - "custom rate limit string"
     prompt_patterns:
       - "Custom prompt pattern"
     exclude_rate_limit_patterns:
       - "429"               # removes "429" from defaults
     exclude_prompt_patterns:
       - "Allow once"        # removes "Allow once" from defaults
     ```
     This avoids having to copy all defaults just to add one pattern, and makes it unambiguous which category an exclusion applies to.
- [x] Per-task override in task YAML:
  ```yaml
  skip_permissions: true  # override global for this task only
  ```
- [x] If `skip_permissions` is false for a task AND running unattended:
  - Log warning: `"⚠️ Task 'X' may hang on permission prompts. Set skip_permissions: true or run interactively."`
  - **Two-tier hang detection** (not just silence-based):
    1. **Prompt-wait detection** (high confidence): triggers only when a **pattern match is followed by silence** — a pattern alone is not enough, because tools can emit similar strings mid-output:
       - Match candidate: last line of stderr/stdout matches one of:
         - `"Do you want to proceed?"` (exact substring)
         - `"Allow once"` / `"Allow always"` (Claude Code permission UI)
         - Line ending with `"(Y/n)"` or `"(y/N)"`
       - Generic line-ending patterns like `"> "` or `"? "` are **excluded** — too many tools emit these legitimately (REPLs, interactive installers, test prompts)
       - **Confirmation gate**: pattern match alone does NOT trigger kill. Kill only if the matched line is followed by **30 seconds of complete silence** (no further stdout/stderr). This prevents false kills when a tool happens to emit a matching string but then continues.
       - Patterns loaded from `matchers.yaml` under `prompt_patterns` key — users can add/remove
       - Tasks with `skip_permissions: true` skip prompt-wait detection entirely
    2. **Silence timeout** (lower confidence): if no stdout/stderr output for `hang_timeout` duration (default `10m`, configurable), assume hung → kill → mark `failed` with reason `"hung_no_output"`
  - Default `10m` (not 5m) — long tool operations like `npm install`, large test suites, or git operations can legitimately be quiet for several minutes
  - The timeout resets on every line of output, so long-running tasks that produce output won't be killed
  - Tasks with `skip_permissions: true` skip prompt-wait detection (no permission prompts possible)
- [x] **First-run safety prompt**: on first `claude-autopilot run`, display:
  ```
  ⚠️  SAFETY NOTICE: Autonomous mode can execute shell commands and modify files
  without confirmation. Only queue prompts you trust.
  
  To enable: set skip_permissions: true in config or per-task YAML.
  Press Enter to continue, Ctrl+C to cancel.
  ```
- [x] **Non-interactive bypass** for CI/cron/headless:
  - `claude-autopilot run --yes` → skip the first-run prompt
  - `CLAUDE_AUTOPILOT_NONINTERACTIVE=1` env var → same effect
  - If stdin is not a TTY (`!term.IsTerminal(fd)`) → auto-skip prompt, log: `"Non-interactive mode detected, skipping safety prompt."`
  - The prompt only appears once anyway — after first run, a `~/.claude-autopilot/.first-run-ack` file is created and future runs skip it
  - `.first-run-ack` is a zero-byte marker file — no content to corrupt, no crash-safety concern. Created via simple `os.Create` + `Close`.
- [x] `--dangerously-skip-permissions` is **never** the default. Users must explicitly opt in.
- [x] README includes a dedicated "Safety" section explaining the risk

## Phase 4: Rate Limit Detection & Auto-Resume (the core feature)

### 4a: Layered Detection Strategy (not just string matching)

Rate limit detection must be resilient across Claude Code CLI versions. Use a **layered approach**, checked in order:

1. **Exit code check** (most reliable):
   - Exit code `75` or specific non-zero codes → likely rate limit
   - Exit code `0` → task completed successfully
   - Map known exit codes in a configurable table
2. **Stderr pattern matching** (primary):
   - Default patterns (shipped with binary):
     - `"Claude usage limit reached"`
     - `"rate_limit_error"`
     - `"Your limit will reset at"`
     - `"rate limit"` (case-insensitive)
     - `"429"` in error context
   - Patterns loaded from merged matchers (see Permission Safety section for merge strategy), user can add custom patterns
3. **Stdout pattern matching** (secondary):
   - Same configurable patterns, lower confidence
4. **Unknown-error classification**:
   - If process exits non-zero and doesn't match any known pattern → classify as `unknown_error`
   - Log full stdout/stderr dump for manual review
   - Default behavior: retry once with backoff, then mark `failed` (configurable)

- [x] Implement `Detector` interface with `Detect(exitCode int, stdout, stderr string) -> DetectionResult`
- [x] `DetectionResult` enum: `RateLimited(resetTime?)`, `Completed`, `Failed(reason)`, `Unknown`
- [x] Load default matchers + merge with user-defined matchers from config
- [x] Parse reset timestamp from output when available (regex: `reset at <time>`)
- [x] **Reset time parsing must be timezone/locale-aware:**
  - Claude Code outputs reset times in **local timezone** with various formats:
    - `"Your limit will reset at 6:30 PM"` (12hr, no date, no TZ)
    - `"resets 6pm"` (abbreviated)
    - `"reset at Oct 7, 1am"` (with date)
    - `"Your limit will reset at 3pm (America/Santiago)"` (with TZ name)
  - [x] Parse using a priority chain:
    1. If TZ name present (e.g. `America/Santiago`) → use it directly
    2. If no TZ → assume system local timezone (`time.Now().Location()` in Go)
    3. If parsed time is in the past → apply **date-aware branching**:
       - If the string contains an explicit date (e.g. `"Oct 7, 1am"`) → trust it as-is even if past (it may be a stale/buggy message from Claude Code — log warning, fall back to exponential backoff)
       - If the string is time-only (e.g. `"6:30 PM"`, `"resets 6pm"`) → assume today first; if past, assume tomorrow (+24h)
       - Never blindly +24h a string that already has a month/year — that could sleep for a day on a month boundary when the real wait is minutes
  - [x] Support both 12hr and 24hr formats
  - [x] If parsing fails entirely → log the raw string + fall back to exponential backoff
  - [x] **Unit tests** for at least: `"6:30 PM"`, `"resets 6pm"`, `"reset at Oct 7, 1am"`, `"3pm (America/Santiago)"`, `"14:30"`, garbage input, time-only-in-past (+24h), date-in-past (fallback to backoff), and month boundary (`"Dec 31, 11pm"` parsed on Jan 1)
- [x] If no timestamp found, use exponential backoff **with jitter**: doubles each time starting at 5min (5 → 10 → 20 → 40 → 80 → 160 → 300min cap), each with ±20% random jitter. Jitter prevents thundering herd when multiple rate-limited instances retry simultaneously.

### 4b: Resume Semantics (exact contract)

When a task is interrupted by a rate limit, resume uses **Claude Code's native session resume**
(`--resume <session-id>`) when available (version-gated), falling back to re-prompt on older CLIs.
This is a major advantage over claude-code-queue which has no session awareness.

- [x] **State is built in two phases per task execution** (all persisted to `<task-id>.state.json`):
  1. **Pre-run** (before spawning subprocess): write initial state:
     - `status`: `running`
     - `prompt_hash`: SHA-256 of the original prompt
     - `git_commit`: current HEAD of the working directory (if git repo)
     - `attempt`: incremented before each execution (starts at 0 in state, so first execution records attempt 1, second records 2, etc.)
     - `started_at`: timestamp
  2. **Post-init** (after receiving `system` NDJSON message): update state with:
     - `session_id`: extracted from the `system` init message
     - `last_ndjson_messages`: updated continuously as messages arrive (keep last 20 lines before interruption — matches re-prompt fallback context window)
  - All fields live in `~/.claude-autopilot/state/<task-id>.state.json` (no separate checkpoint file)
  - If crash occurs between phase 1 and 2 (subprocess started but no `system` message yet), state has no `session_id` → resume falls back to re-prompt strategy
- [x] **On resume after rate limit**:
  - **First attempt** (if CLI supports `--resume` per compat table):
    Use `claude --resume <session-id> --print --output-format stream-json --verbose`
    This natively continues the exact conversation where it left off — no duplicate work
  - **If `--resume` not available** (old CLI) or **`--resume` fails** (session expired/invalid):
    Fall back to re-prompt with context:
    ```
    [RESUMED — attempt {N}. Previous session expired.
    Last output before interruption: {last 20 lines}.
    Continue from where you left off. Do not redo completed work.]
    ```
  - Verify `git_commit` still matches HEAD (warn if code changed externally)
  - Log the resume strategy used (native vs. re-prompt) for auditability
- [x] **Idempotency safeguards**:
  - If task was fully completed before rate limit message appeared → detect via `type: "result"` in output, mark as `done`
  - If working dir has uncommitted changes from partial run → include in resume context
  - Max retries per task (default: 5) before marking as `failed`
- [x] Display countdown timer in terminal:
  ```
  ⏳ Rate limited. Resuming at 6:30 PM (2h 14m remaining)
  ██████████░░░░░░░░░░ 48%
  Task: "Setup auth module" | Attempt 2/5 | Session: abc-123-def
  ```

## Phase 5: Logging & Notifications

- [x] Per-task log files in `~/.claude-autopilot/logs/<task-id>.log`
  - **Log rotation**: max 10MB per task log. When exceeded, rotate to `<task-id>.log.1` (keep 1 backup). `clean` removes rotated backups.
- [x] Summary log: which tasks ran, duration, retries, final status
- [x] Terminal bell (`\a`) when all tasks complete
- [x] Optional: webhook notification (Slack, Discord, Telegram)
  - [x] `claude-autopilot config set webhook_url https://hooks.slack.com/...`
- [x] Optional: desktop notification via `osascript` (macOS) or `notify-send` (Linux)
- [x] **Notification failure handling**: if webhook or desktop notification fails, log warning and continue — never fail the run because of a notification error. Retry webhook once after 5s on network error.
- [x] Print summary on completion:
  ```
  ✅ Task 1: Setup auth module — Done (23 min, 1 retry)
  ✅ Task 2: Add database migrations — Done (12 min, 0 retries)
  ❌ Task 3: Integrate Stripe — Failed after 3 retries
  ⏱️  Total time: 4h 12m (including 2h 30m rate limit wait)
  ```

## Phase 6: Conductor Compatibility (design TBD)

> **Note**: Conductor's internal architecture and rate-limit signaling are not yet documented.
> This phase requires investigation once Conductor's API/log format is understood.
> The items below are directional goals, not concrete implementation tasks.

- [x] Investigate how to detect Conductor rate limits (process monitoring? log tailing? API?)
- [x] Support "watch mode": monitor Conductor workspaces for rate limits
- [x] When Conductor's Claude Code agents get rate-limited:
  - Pause gracefully
  - Wait for reset
  - Trigger resume/retry
- [x] Alternative workflow: use claude-autopilot as a pre-step — queue tasks,
      feed them to Claude Code one by one, review diffs in Conductor after

## Phase 7: Polish & Release

- [x] Write README with:
  - Install instructions (Go install + Homebrew)
  - Usage examples
  - Demo GIF / asciinema recording
  - Comparison with claude-code-queue
- [x] `Makefile` with build, install, release targets
- [x] Cross-compile: macOS (arm64/amd64), Linux (amd64/arm64), Windows (amd64) — matches all platforms claimed in spec (flock, atomic writes, temp cleanup)
- [x] GoReleaser config for GitHub releases
- [x] Tag `v0.1.0`, push, share

---

## Tech Stack

| Component           | Choice                              |
|---------------------|-------------------------------------|
| Language            | Go                                  |
| Subprocess mgmt     | `os/exec` (stdlib)                  |
| CLI framework       | `spf13/cobra`                       |
| Config / Task files  | `spf13/viper` + `gopkg.in/yaml.v3`  |
| Logging             | `slog` (stdlib) or `charmbracelet/log` |
| Terminal UI          | `charmbracelet/bubbletea` (countdown) |
| Notifications        | `gen2brain/beeep` (cross-platform)  |
| Process locking      | `flock` via `syscall` (Linux/macOS), `golang.org/x/sys/windows` |
| Time parsing         | `time` stdlib + custom locale-aware parser |

## MVP = Phase 1-5

> Ship Phase 1-5 first. That's your working tool.
> Phase 6 (Conductor) is a nice addon.
> Phase 7 is for public release.

### MVP Acceptance Criteria

Every item must pass before tagging `v0.1.0`:

| Command | Acceptance Test |
|---------|----------------|
| `claude-autopilot add "prompt" --dir .` | Creates a valid `.yaml` task file with auto-generated ID, resolves working_dir to absolute path, validates it exists, appears in `list` output |
| `claude-autopilot list` | Shows all tasks in deterministic execution order with correct status |
| `claude-autopilot run` | Acquires lock, executes pending tasks in priority order, spawns Claude Code, streams output. Reloads queue between tasks (new tasks picked up). If only future `waiting` tasks remain, sleeps until nearest `resume_at` and continues (does not exit early). |
| `claude-autopilot --project-dir /path run` | Uses specified directory for project-local tasks instead of cwd. Also works with `list`, `status`, `clean`, `retry`, `cancel`. |
| `claude-autopilot run` (second instance) | Attempts flock, gets EWOULDBLOCK, reads PID from lockfile (with retry if empty/race), exits with "already running" message |
| `claude-autopilot run` (rate limited) | Detects rate limit (layered), parses reset time (timezone-aware), shows countdown, auto-resumes via `--resume`, completes task |
| `claude-autopilot run` (unknown error) | Classifies error, logs output, retries once, then marks `failed` |
| `claude-autopilot run` (old Claude Code) | Detects version, falls back to text mode + re-prompt resume |
| `claude-autopilot run` (SIGTERM) | Graceful shutdown: kills subprocess, saves state, releases lock, exits 130 |
| `claude-autopilot status` | Shows current run state (no runner lock needed): active task, queue depth, next resume time. Handles partial lockfile gracefully (shows "PID unknown"). May create `.init.json` for new tasks on first encounter (same as `list`). |
| `claude-autopilot retry <task-id>` | Re-queues a `failed`/`cancelled` task with reset attempt counter. If `run` is inactive, invalid states error immediately. If `run` is active, command is queued and validated at apply-time by runner (incompatible state → dropped with info log). |
| `claude-autopilot cancel <task-id>` | Sets `pending`/`waiting`/`failed` task to `cancelled`, skipped by future `run`. No-op on `done`/`cancelled`. If `run` is active, command is queued and applied by runner. If task is currently running, prints advisory message. |
| `claude-autopilot clean` | Cleans artifacts (orphan temp files, rotated log backups) without deleting task state files (`.init.json`, `.state.json`). |
| Crash recovery | Kill -9 mid-run → kernel releases flock automatically → restart → acquires lock → reloads state from disk → detects stale `running` state → reverts to `pending` → resumes with `--resume` if `session_id` present. |
| State persistence | All task states survive restarts (verified by restarting between tasks) |
| State machine | `pending→running→done`, `running→waiting→running→done`, `running→failed`, `failed→pending` (via retry), `pending→cancelled→pending` (via retry), `waiting→cancelled` (via cancel), `failed→cancelled` (via cancel) |
| Permission safety | Default: skip_permissions OFF. Task with skip_permissions=true passes flag. Task without it: prompt-wait pattern + 30s silence → kill; silence >10min (no pattern) → kill. |
| Headless/CI mode | `--yes` flag or non-TTY stdin skips first-run prompt. Works in cron, Docker, CI. |
| `claude-autopilot config set/get/list` | `set` persists to config.yaml (atomic write) and rejects unknown keys, `get` shows resolved value with source layer, `list` shows all keys. |
| Exit codes | `run` exits 0 (all done), 1 (any failed), 2 (lock contention/fatal error), 130 (SIGINT/SIGTERM). Other commands exit 0 on success, 1 on error. |

### Testing Plan

- [x] **Unit tests**:
  - `detector`: mock exit codes + output strings, version-dependent behavior
  - `timeparse`: all reset time formats (`"6:30 PM"`, `"resets 6pm"`, `"3pm (America/Santiago)"`, `"14:30"`, garbage input, time-only-in-past → +24h, date-in-past → fallback, month boundary)
  - `queue`: CRUD, ordering (priority ties, mixed sources, duplicate ID detection, init.json hardlink race simulation, hardlink-unsupported → clear error message, PID-scoped orphan tmp cleanup, queue reload picks up new tasks, wait-loop tick reload picks pending tasks within <=30s, duplicate across standalone/tasks.yaml/within-tasks.yaml)
  - `compat`: version parsing, range matching, no-match → safe mode, independent resume/output fallbacks
  - `lock`: acquire (flock LOCK_EX|LOCK_NB), detect contention (EWOULDBLOCK → read PID with retry for empty file → refuse), verify auto-release on process death (spawn child via `exec.Command` that holds lock → `cmd.Process.Kill()` → parent acquires — portable across Linux/macOS/Windows)
  - `state`: state machine transitions (valid: pending→running, waiting→cancelled, failed→cancelled, invalid: done→running), crash recovery (stale `running` → `pending`), retry (failed→pending with counter reset), cancel (pending→cancelled, waiting→cancelled, failed→cancelled, done→no-op, cancelled→no-op)
  - `control`: command queue append/read/apply (`retry`/`cancel` while runner active), idempotency on duplicate commands, malformed-line skip with warning, post-apply compaction/truncate behavior, Windows append locking
  - `resume`: session_id extraction, native resume vs re-prompt fallback, missing session_id → re-prompt
  - `hang`: prompt-wait pattern + silence gate (pattern followed by output → no kill; pattern followed by 30s silence → kill)
  - `config`: resolution order (task-scoped keys: CLI > task YAML > env > config > default; global-only keys: CLI > env > config > default), `set` atomic write + key validation, `get` shows source layer, `list` all keys, config loaded once per `run` (not reloaded mid-execution)
  - `matchers`: default + user merge, per-category exclude_rate_limit_patterns / exclude_prompt_patterns
  - `fileutil`: atomic temp+rename (verify no partial file on simulated crash), temp+hardlink (verify EEXIST race safety), temp file naming (`<file>.tmp.<pid>.<random>`), orphan temp cleanup (dead PID → delete, alive PID → skip, mtime > 24h → delete)
  - `notifier`: webhook retry on failure (1 retry after 5s), desktop notification failure → warning only, bell output
  - `add`: field validation (missing prompt → error, relative working_dir → error, non-existent dir → error), absolute path resolution, ID generation (slugify + 4 hex), title default (first 60 chars of prompt)
- [x] **Integration test**: mock Claude Code binary (bash script that simulates rate limit after N seconds, then succeeds on retry)
- [x] **Integration test**: mock Claude Code binary (old version — no stream-json support, verify fallback works)
- [x] **Integration test**: graceful shutdown (spawn `run` → send SIGTERM → verify subprocess killed, state saved, lock released)
- [x] **Integration test** (POSIX only): `retry`/`cancel` while `run` is active → verify control command queued and applied within one tick
- [x] **Manual smoke test script**: `test/smoke.sh` that runs full add → run → rate-limit → resume → done cycle with mock binary (POSIX only; CI runs smoke tests on Linux)
- [x] **CI**: GitHub Actions running `go test ./...` + smoke test on push

---

## Estimated Timeline

| Phase   | Effort       | Notes |
|---------|-------------|-------|
| Phase 1 | 30 min      | Setup, scaffold, deps |
| Phase 2 | 2.5 hrs     | YAML schema, parser, state machine, ordering, lock, CLI commands (add/list/status/retry/cancel/clean) |
| Phase 3 | 2 hrs       | Subprocess lifecycle, NDJSON parsing, version compat layer, permission policy, graceful shutdown |
| Phase 4 | 2.5 hrs     | Layered detection, timezone-aware time parsing, state/resume, countdown |
| Phase 5 | 1 hr        | Logging (with rotation), notifications, summary |
| Tests   | 2.5 hrs     | Unit (timeparse, detector, queue, compat, lock, state, resume, config) + integration + smoke |
| **MVP** | **~11 hrs** | **Realistic with all safety + compat + testing** |
| Phase 6 | 2-3 hrs     | Conductor integration |
| Phase 7 | 1-2 hrs     | GoReleaser, cross-compile, README polish |
| Phase 8 | 1-2 hrs     | SEO, llms.txt, launch posts |

---

## Phase 8: SEO, Discoverability & Branding

> The README and repo metadata should be optimized for **3 audiences**: Google, GitHub search, and LLMs (ChatGPT/Claude/Perplexity that recommend tools).

- [x] **GitHub repo description** (one-liner, keyword-rich):
  - `"Autonomous task runner for Claude Code — auto-retries on rate limits, queues tasks, keeps working while you sleep."`
- [x] **GitHub topics/tags** (max 20):
  - `claude`, `claude-code`, `anthropic`, `rate-limit`, `auto-retry`, `task-queue`, `cli`, `golang`, `developer-tools`, `ai-coding`, `autonomous`, `conductor`, `claude-code-queue`, `agentic`, `automation`
- [x] **README structure optimized for all 3 engines**:
  - Hero section: name + one-liner + badges (Go version, license, stars)
  - "Why?" section: 2-3 sentences explaining the pain point (relatable, searchable)
  - Quick demo GIF (GitHub rewards visual READMEs with more impressions)
  - Install: one-liner `go install` + Homebrew
  - Usage: 3 clear examples (add task, run queue, check status)
  - How it works: simple diagram (the ASCII one from this doc)
  - Config reference
  - Comparison table vs alternatives
  - Contributing + License
- [x] **Keywords to naturally include in README** (for Google + LLM indexing):
  - "Claude Code rate limit", "auto retry Claude", "Claude usage limit workaround"
  - "run Claude Code overnight", "Claude Code task queue", "autonomous coding agent"
  - "Claude rate limit reset", "Claude Code automation", "unattended Claude Code"
- [x] **llms.txt file** in repo root (emerging standard for LLM discoverability):
  ```
  # claude-autopilot
  > Autonomous task runner for Claude Code. Queues coding tasks, detects rate limits, waits for reset, and auto-resumes — so Claude keeps working while you sleep.
  
  ## What it does
  - Wraps Claude Code CLI as a subprocess
  - Monitors output for rate limit messages
  - Automatically waits and retries when limits reset
  - Queues multiple tasks with priority ordering
  - Sends notifications when work completes
  
  ## Install
  go install github.com/hseinmoussa/claude-autopilot@latest
  ```
- [x] **Social launch posts** (draft for each platform):
  - Twitter/X: short + GIF + relevant hashtags
  - Reddit r/ClaudeAI + r/ClaudeCode: pain-point-first framing
  - Hacker News: "Show HN: claude-autopilot — keeps Claude Code working while you sleep"
- [x] Add `FUNDING.yml` if you want GitHub Sponsors later

---

## Research Findings: claude-code-queue Analysis

> What we learned from analyzing the existing [claude-code-queue](https://github.com/JCSnap/claude-code-queue) (Python)
> and the [Claude Agent SDK Spec](https://gist.github.com/SamSaffron/603648958a8c18ceae34939a8951d417).

### What they do well ✅
- Markdown + YAML frontmatter for task definitions (proven UX, we adopted YAML-only)
- Priority queue with `max_retries` per task
- CLI interface: `add`, `start`, `status`, `list`, `cancel`, `template`, `bank`
- Prompt bank for reusable templates (good feature, we should add)

### Where they fall short ❌ (our opportunities)
| Gap | claude-code-queue | claude-autopilot (ours) |
|-----|-------------------|------------------------|
| Rate limit wait | "Naively loop every fixed interval" — polls blindly | Parse reset timestamp + exponential backoff with jitter |
| Session resume | No session awareness — re-runs prompt from scratch | Uses `--resume <session-id>` for native continuation |
| Output parsing | Text-based output monitoring | NDJSON structured parsing via `--output-format stream-json` |
| Completion detection | String matching on output | Typed `"result"` message in NDJSON stream |
| Notifications | None | Terminal bell, desktop notification, webhook |
| Language / binary | Python (requires pip install + deps) | Single Go binary, zero dependencies |
| Testing | No test suite visible | Unit + integration + smoke tests planned |
| Conductor compat | None | Planned Phase 6 |

### Key technical insight: Claude Code's `--resume` flag
The Claude Agent SDK spec reveals that `claude --resume <session-id>` natively continues
an interrupted session. This is a **game-changer** — it means we don't need to hack together
resume prompts. We just save the `session_id` from the init message and pass it back on retry.
claude-code-queue doesn't use this at all.

### Borrowed ideas (with credit)
- Prompt bank / template system → add to Phase 6 or 7
- `estimated_tokens` field in task schema → reserved in YAML schema, implement cost tracking post-MVP

---

## Similar Projects

| Project | Language | Difference |
|---------|----------|------------|
| [claude-code-queue](https://github.com/JCSnap/claude-code-queue) | Python | Similar concept, Python-based, less polished |
| [claude-flow](https://github.com/ruvnet/claude-flow) | JS | Broader orchestration, feature requested |
| **claude-autopilot (ours)** | **Go** | **Fast binary, better UX, notifications, Conductor-aware** |
