# claude-autopilot

[![CI](https://github.com/hseinmoussa/claude-autopilot/actions/workflows/ci.yml/badge.svg)](https://github.com/hseinmoussa/claude-autopilot/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/hseinmoussa/claude-autopilot)](https://goreportcard.com/report/github.com/hseinmoussa/claude-autopilot)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

Autonomous task runner for Claude Code that queues tasks, detects rate limits, waits for reset, and auto-resumes -- so work keeps moving while you sleep.

## Why

You're working with Claude Code, hit the usage limit, and have to wait hours. You want to walk away and have it **automatically resume** when the limit resets.

`claude-autopilot` automates that loop: queue tasks, run them in priority order, detect rate limits, wait for reset, and continue unattended. Come back to finished work.

## Install

```bash
go install github.com/hseinmoussa/claude-autopilot@latest
```

Or download a prebuilt binary from [Releases](https://github.com/hseinmoussa/claude-autopilot/releases).

Or build from source:

```bash
git clone https://github.com/hseinmoussa/claude-autopilot.git
cd claude-autopilot
make build
```

### Prerequisites

- [Claude Code CLI](https://docs.anthropic.com/en/docs/claude-code) must be installed and authenticated (`claude` command available in PATH)
- Go 1.24+ (only needed if building from source)

## Quick Start

**1. Add a task:**

```bash
claude-autopilot add "Fix auth bug and add tests" --dir /path/to/project --priority 1
```

**2. Run the queue:**

```bash
claude-autopilot run --yes
```

**3. Check status:**

```bash
claude-autopilot status
```

That's it. If Claude hits a rate limit, `claude-autopilot` detects it, waits for reset, and resumes automatically.

## How It Works

```
+--------------+     +------------------+     +---------------+
|  Task Queue  |---->| claude-autopilot |---->|  Claude Code  |
| (YAML files) |     |   (Go binary)    |     | (subprocess)  |
+--------------+     +--------+---------+     +---------------+
                              |
                       Rate limit hit?
                              |
                     +--------v--------+
                     | Wait for reset  |
                     | then auto-resume|
                     +-----------------+
```

1. You define tasks as YAML files or via the `add` CLI command
2. `claude-autopilot` spawns Claude Code as a subprocess for each task
3. If rate-limited: parses the reset time, sleeps, auto-resumes (using `--resume` for session continuity when available)
4. Logs everything so you can review what happened
5. Sends a notification (terminal bell, webhook, desktop) when done

### Rate Limit Detection

Detection uses a layered strategy (checked in order):

1. **Exit code** -- exit code 75 = rate limited, exit code 0 = success
2. **Stderr patterns** -- matches configurable strings like "rate limit", "429", "usage limit reached"
3. **Stdout patterns** -- same patterns, lower confidence
4. **Reset time parsing** -- extracts the reset time from output (timezone-aware, 12hr/24hr formats)
5. **Fallback** -- exponential backoff with jitter (5 min base, doubles, 5 hour cap)

### Session Resume

When Claude Code supports `--resume` (v2.0+), `claude-autopilot` saves the session ID and resumes the exact conversation after a rate limit. On older versions, it falls back to a re-prompt strategy that includes context from the interrupted session.

## Commands

| Command | Description |
|---------|-------------|
| `add <prompt> --dir <path>` | Add a new task to the queue |
| `run` | Start executing the task queue |
| `list` | Show all tasks in execution order |
| `status` | Show runner state and queue summary |
| `retry <id>` | Re-queue a failed or cancelled task |
| `cancel <id>` | Cancel a pending, waiting, or failed task |
| `clean` | Remove orphan temp files and rotated logs |
| `config set\|get\|list\|path` | Manage configuration |

### Adding Tasks

```bash
# Basic usage
claude-autopilot add "Implement user authentication" --dir /path/to/project

# With options
claude-autopilot add "Add API endpoints" \
  --dir /path/to/project \
  --priority 1 \
  --model claude-sonnet-4-5-20250929 \
  --title "API endpoints" \
  --skip-permissions
```

### Task YAML Format

You can also define tasks as YAML files in `~/.claude-autopilot/tasks/` or `.autopilot/tasks/` (project-local):

```yaml
id: setup-auth-module
title: "Setup auth module"
priority: 1
working_dir: /path/to/project
skip_permissions: true
prompt: |
  Create a JWT authentication module with login/signup endpoints.
  Use Go + chi router. Write tests.
context_files:
  - src/routes.go
  - docs/auth-spec.md
model: claude-sonnet-4-5-20250929
max_retries: 5
```

Multiple tasks can be defined in a single file using YAML document separators (`---`). See [tasks.yaml.example](tasks.yaml.example) for a full example.

### Task Priority and Ordering

Tasks execute in deterministic order: `priority ASC, created_at ASC, id ASC`.

- Lower priority number = runs first (default: 10)
- Equal priority = earlier creation time wins (FIFO)
- Both equal = alphabetical by ID

## Configuration

Config is stored in `~/.claude-autopilot/config.yaml`. Values can also be set via environment variables (`CLAUDE_AUTOPILOT_<KEY>`).

| Key | Default | Description |
|-----|---------|-------------|
| `skip_permissions` | `false` | Pass `--dangerously-skip-permissions` to Claude Code |
| `hang_timeout` | `10m` | Kill task if no output for this duration |
| `webhook_url` | (empty) | POST JSON notification on completion |
| `notification_desktop` | `false` | Send native desktop notification on completion |
| `notification_bell` | `true` | Ring terminal bell on completion |

```bash
# Set a webhook for Slack/Discord notifications
claude-autopilot config set webhook_url https://hooks.slack.com/services/...

# View all config values and their sources
claude-autopilot config list
```

### Rate Limit Patterns

Default detection patterns are built-in. You can extend or override them by creating `~/.claude-autopilot/matchers.yaml`:

```yaml
rate_limit_patterns:
  - "custom rate limit string"
prompt_patterns:
  - "Custom prompt pattern"
exclude_rate_limit_patterns:
  - "429"  # remove "429" from defaults
```

## Safety

**`--dangerously-skip-permissions` is OFF by default.** Claude Code will hang on permission prompts in unattended mode unless you explicitly enable it.

Options:
- Set `skip_permissions: true` globally in config
- Set `skip_permissions: true` per-task in YAML
- Pass `--skip-permissions` on `add`

When skip_permissions is off, `claude-autopilot` has two-tier hang detection:
1. **Prompt pattern detection**: if output matches a permission prompt pattern followed by 30s of silence, the task is killed
2. **Silence timeout**: if no output for `hang_timeout` (default 10 min), the task is killed

On first run, a safety acknowledgement prompt is displayed. Use `--yes` or set `CLAUDE_AUTOPILOT_NONINTERACTIVE=1` to bypass it in CI/cron.

## Project Structure

```
claude-autopilot/
  cmd/root.go              # CLI commands (add, run, list, status, retry, cancel, clean, config)
  internal/
    queue/                  # Task queue, YAML parsing, ordering, state machine
    runner/                 # Claude Code subprocess runner, graceful shutdown
    compat/                 # CLI version detection, adapter interface
    detector/               # Rate limit detection (layered)
    resume/                 # Resume strategy (native --resume vs re-prompt)
    timeparse/              # Timezone-aware reset time parser
    lock/                   # flock-based process locking
    fileutil/               # Atomic write + fsync helpers
    notifier/               # Notifications (bell, webhook, desktop)
    config/                 # Config loading + matchers
  test/
    smoke.sh                # End-to-end smoke test
    integration.sh          # Integration tests
    mock_claude.sh          # Mock Claude CLI for testing
  main.go
  tasks.yaml.example
```

## Development

```bash
# Run unit tests
go test ./...

# Run smoke test (builds binary + tests with mock Claude)
make smoke

# Run integration tests
bash test/integration.sh

# Run all CI checks
make ci

# Lint
make lint
```

## Contributing

1. Fork the repo
2. Create a feature branch (`git checkout -b feature/my-feature`)
3. Make your changes and add tests
4. Run `make ci` to verify everything passes
5. Submit a pull request

Please open an issue first for significant changes to discuss the approach.

## License

[MIT](LICENSE)
