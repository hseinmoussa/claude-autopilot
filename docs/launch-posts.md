# Launch Posts

## X / Twitter

claude-autopilot is live: an autonomous task runner for Claude Code.

- queues tasks
- auto-detects rate limits
- waits + resumes automatically
- keeps working while you sleep

Install: `go install github.com/hseinmoussa/claude-autopilot@latest`

#claude #claudecode #golang #devtools #automation

## Reddit (r/ClaudeAI / r/ClaudeCode)

Title: Show r/ClaudeCode: claude-autopilot (auto-retry + auto-resume for rate limits)

I built a Go CLI that wraps Claude Code with a deterministic task queue and automatic resume behavior.

Main features:
- global + project-local YAML task sources
- rate limit detection (exit code + output patterns + reset-time parsing)
- state persistence with crash recovery
- retry/cancel control commands while running
- smoke + integration tests

Install:
`go install github.com/hseinmoussa/claude-autopilot@latest`

Would love feedback on workflow and edge cases.

## Hacker News

Title: Show HN: claude-autopilot – queue Claude Code tasks and auto-resume after rate limits

I wanted a way to queue multiple Claude Code tasks overnight without babysitting resets.

Built a Go CLI that:
- runs tasks in deterministic order
- persists runtime state safely
- detects rate limits and waits until reset
- resumes session-aware when possible
- supports retry/cancel controls while running

Repo: https://github.com/hseinmoussa/claude-autopilot
