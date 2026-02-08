package compat

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// CompatEntry describes the capabilities of a specific Claude CLI version range.
type CompatEntry struct {
	MinVersion        string // inclusive semver lower bound (e.g. "2.0.0")
	MaxVersion        string // inclusive semver upper bound (e.g. "2.99.99")
	StreamJSON        bool   // supports --output-format stream-json
	ResumeFlag        bool   // supports --resume / session continuation
	ExitCodeRateLimit int    // exit code emitted on rate limit (-1 = not supported)
}

// defaultCompat is the built-in compatibility table, ordered newest first.
var defaultCompat = []CompatEntry{
	{
		MinVersion:        "2.0.0",
		MaxVersion:        "2.99.99",
		StreamJSON:        true,
		ResumeFlag:        true,
		ExitCodeRateLimit: 75,
	},
	{
		MinVersion:        "1.0.0",
		MaxVersion:        "1.99.99",
		StreamJSON:        false,
		ResumeFlag:        false,
		ExitCodeRateLimit: -1,
	},
}

// DetectVersion runs `claude --version` and returns the parsed version string.
// The output is expected to contain a semver-like version (e.g. "claude 2.1.3").
func DetectVersion() (string, error) {
	out, err := exec.Command("claude", "--version").Output()
	if err != nil {
		return "", fmt.Errorf("run claude --version: %w", err)
	}
	return parseVersionOutput(string(out))
}

// parseVersionOutput extracts a semver version from command output.
// It looks for a token that starts with a digit and contains dots.
func parseVersionOutput(output string) (string, error) {
	fields := strings.Fields(strings.TrimSpace(output))
	for _, f := range fields {
		if len(f) == 0 {
			continue
		}
		// Strip leading 'v' if present.
		candidate := strings.TrimPrefix(f, "v")
		if len(candidate) > 0 && candidate[0] >= '0' && candidate[0] <= '9' && strings.Contains(candidate, ".") {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("no version found in output: %q", output)
}

// LookupCompat finds the CompatEntry matching the given version string.
// Returns nil if no entry matches (unknown or newer version).
func LookupCompat(version string) (*CompatEntry, error) {
	for i := range defaultCompat {
		entry := &defaultCompat[i]
		if CompareSemver(version, entry.MinVersion) >= 0 && CompareSemver(version, entry.MaxVersion) <= 0 {
			return entry, nil
		}
	}
	return nil, nil
}

// CLIAdapter provides version-specific behavior for building CLI commands.
type CLIAdapter interface {
	// BuildArgs constructs the argument list for invoking the Claude CLI.
	BuildArgs(prompt string, model string, sessionID string, skipPerms bool, extraFlags []string) []string
	// SupportsStreamJSON reports whether the CLI supports stream-json output.
	SupportsStreamJSON() bool
	// SupportsResume reports whether the CLI supports native session resume.
	SupportsResume() bool
	// RateLimitExitCode returns the exit code used for rate limits, or -1.
	RateLimitExitCode() int
}

// NewAdapter creates a CLIAdapter from a CompatEntry. If entry is nil (unknown
// version), a safe-mode adapter is returned that optimistically tries modern
// features.
func NewAdapter(entry *CompatEntry) CLIAdapter {
	if entry == nil {
		return &safeAdapter{}
	}
	return &knownAdapter{entry: entry}
}

// knownAdapter implements CLIAdapter for a known CLI version range.
type knownAdapter struct {
	entry *CompatEntry
}

func (a *knownAdapter) BuildArgs(prompt, model, sessionID string, skipPerms bool, extraFlags []string) []string {
	args := []string{"--print"}

	if a.entry.StreamJSON {
		// Claude CLI requires --verbose with stream-json in print mode.
		args = append(args, "--verbose", "--output-format", "stream-json")
	}

	if a.entry.ResumeFlag && sessionID != "" {
		args = append(args, "--resume", sessionID)
	}

	if model != "" {
		args = append(args, "--model", model)
	}

	if skipPerms {
		args = append(args, "--dangerously-skip-permissions")
	}

	args = append(args, extraFlags...)
	// Use "--" to separate flags from the prompt positional argument,
	// preventing prompts starting with "-" from being misinterpreted as flags.
	args = append(args, "--", prompt)
	return args
}

func (a *knownAdapter) SupportsStreamJSON() bool { return a.entry.StreamJSON }
func (a *knownAdapter) SupportsResume() bool     { return a.entry.ResumeFlag }
func (a *knownAdapter) RateLimitExitCode() int   { return a.entry.ExitCodeRateLimit }

// safeAdapter is used when the CLI version is unknown. It optimistically tries
// modern features (stream-json, resume) since they degrade gracefully.
type safeAdapter struct{}

func (a *safeAdapter) BuildArgs(prompt, model, sessionID string, skipPerms bool, extraFlags []string) []string {
	args := []string{"--print"}

	// Optimistically try stream-json; if CLI doesn't support it, it will error
	// and we can fall back.
	args = append(args, "--verbose", "--output-format", "stream-json")

	if sessionID != "" {
		args = append(args, "--resume", sessionID)
	}

	if model != "" {
		args = append(args, "--model", model)
	}

	if skipPerms {
		args = append(args, "--dangerously-skip-permissions")
	}

	args = append(args, extraFlags...)
	args = append(args, "--", prompt)
	return args
}

func (a *safeAdapter) SupportsStreamJSON() bool { return true }
func (a *safeAdapter) SupportsResume() bool     { return true }
func (a *safeAdapter) RateLimitExitCode() int   { return 75 }

// CompareSemver compares two semver strings (MAJOR.MINOR.PATCH).
// Returns -1 if a < b, 0 if a == b, 1 if a > b.
// Pre-release suffixes and build metadata are ignored.
func CompareSemver(a, b string) int {
	aParts := parseSemverParts(a)
	bParts := parseSemverParts(b)

	for i := 0; i < 3; i++ {
		if aParts[i] < bParts[i] {
			return -1
		}
		if aParts[i] > bParts[i] {
			return 1
		}
	}
	return 0
}

// parseSemverParts splits a version string into [major, minor, patch] integers.
// Non-numeric suffixes (e.g. "-beta") are stripped. Missing parts default to 0.
func parseSemverParts(v string) [3]int {
	v = strings.TrimPrefix(v, "v")
	// Strip pre-release suffix (anything after first hyphen).
	if idx := strings.Index(v, "-"); idx >= 0 {
		v = v[:idx]
	}
	// Strip build metadata (anything after +).
	if idx := strings.Index(v, "+"); idx >= 0 {
		v = v[:idx]
	}

	var parts [3]int
	fields := strings.SplitN(v, ".", 4)
	for i := 0; i < 3 && i < len(fields); i++ {
		n, err := strconv.Atoi(fields[i])
		if err == nil {
			parts[i] = n
		}
	}
	return parts
}
