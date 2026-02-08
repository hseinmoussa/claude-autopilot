# Using claude-autopilot with Conductor

[Conductor](https://conductor.build) lets you run multiple coding agents in parallel. `claude-autopilot` complements this by managing sequential task execution with automatic rate-limit recovery.

## Workflow

- **Add tasks from any workspace**: Run `claude-autopilot add "task description" --dir .` in your Conductor workspace to queue work
- **Global queue execution**: Tasks from all workspaces share one queue in `~/.claude-autopilot/tasks/`; start execution with `claude-autopilot run --yes`
- **Unattended operation**: The runner auto-detects rate limits, waits for reset, and resumes -- leave it running overnight across multiple workspaces
- **Check status anytime**: Use `claude-autopilot status` to see progress, next resume time, and which workspace task is currently running
- **Priority control**: Set `--priority N` when adding tasks to control execution order across Conductor workspaces (lower numbers run first)

## Project-local tasks

You can also define tasks in `.autopilot/tasks/` within any project directory. These are loaded alongside global tasks when you run with `--project-dir`:

```bash
claude-autopilot run --yes --project-dir /path/to/project
```
