package detector

import (
	"regexp"
	"strings"
	"time"

	"github.com/hseinmoussa/claude-autopilot/internal/timeparse"
)

// DetectionResult classifies the outcome of a Claude CLI invocation.
type DetectionResult int

const (
	// Unknown means the detector could not classify the result.
	Unknown DetectionResult = iota
	// Completed means the CLI finished successfully.
	Completed
	// RateLimited means a rate limit was detected.
	RateLimited
	// Failed means the CLI exited with a non-rate-limit error.
	Failed
)

// String returns a human-readable label for the detection result.
func (d DetectionResult) String() string {
	switch d {
	case Completed:
		return "completed"
	case RateLimited:
		return "rate_limited"
	case Failed:
		return "failed"
	default:
		return "unknown"
	}
}

// RateLimitResult contains the full detection outcome.
type RateLimitResult struct {
	Result    DetectionResult
	ResetTime *time.Time // non-nil if a reset time could be extracted
	Reason    string     // human-readable explanation of detection
}

// Detector inspects CLI exit codes and output to detect rate limits.
type Detector struct {
	patterns           []string
	rateLimitExitCode  int
	resetTimeRegex     *regexp.Regexp
}

// NewDetector creates a Detector with the given stderr/stdout patterns and
// the expected rate-limit exit code (-1 if exit code detection is disabled).
func NewDetector(patterns []string, rateLimitExitCode int) *Detector {
	return &Detector{
		patterns:          patterns,
		rateLimitExitCode: rateLimitExitCode,
		resetTimeRegex:    regexp.MustCompile(`(?i)(?:will\s+)?reset\s+(?:at\s+)?(.+?)(?:\.|$)`),
	}
}

// Detect analyzes the exit code, stdout, and stderr of a completed CLI
// invocation and returns a layered detection result.
//
// Detection layers (in priority order):
//  1. Exit code: 0 = success, rateLimitExitCode = rate limited
//  2. Stderr pattern matching (high confidence)
//  3. Stdout pattern matching (lower confidence)
//  4. Unknown error classification
func (d *Detector) Detect(exitCode int, stdout, stderr string) RateLimitResult {
	// Layer 1: Exit code.
	if exitCode == 0 {
		return RateLimitResult{
			Result: Completed,
			Reason: "exit code 0",
		}
	}

	if d.rateLimitExitCode >= 0 && exitCode == d.rateLimitExitCode {
		resetTime := d.extractResetTime(stderr + " " + stdout)
		return RateLimitResult{
			Result:    RateLimited,
			ResetTime: resetTime,
			Reason:    "exit code matches rate limit code",
		}
	}

	// Layer 2: Stderr pattern matching (high confidence).
	if pattern, matched := d.matchPatterns(stderr); matched {
		resetTime := d.extractResetTime(stderr + " " + stdout)
		return RateLimitResult{
			Result:    RateLimited,
			ResetTime: resetTime,
			Reason:    "stderr matched pattern: " + pattern,
		}
	}

	// Layer 3: Stdout pattern matching (lower confidence).
	if pattern, matched := d.matchPatterns(stdout); matched {
		resetTime := d.extractResetTime(stdout)
		return RateLimitResult{
			Result:    RateLimited,
			ResetTime: resetTime,
			Reason:    "stdout matched pattern: " + pattern,
		}
	}

	// Layer 4: Unknown error classification.
	return RateLimitResult{
		Result: Failed,
		Reason: "non-zero exit code with no rate limit indicators",
	}
}

// matchPatterns performs case-insensitive substring matching against the
// configured patterns. Returns the first matching pattern and true, or
// empty string and false.
func (d *Detector) matchPatterns(text string) (string, bool) {
	lower := strings.ToLower(text)
	for _, p := range d.patterns {
		if strings.Contains(lower, strings.ToLower(p)) {
			return p, true
		}
	}
	return "", false
}

// extractResetTime attempts to find and parse a reset time from output text.
// Returns nil if no parseable reset time is found.
func (d *Detector) extractResetTime(text string) *time.Time {
	matches := d.resetTimeRegex.FindStringSubmatch(text)
	if matches == nil || len(matches) < 2 {
		return nil
	}

	timeStr := strings.TrimSpace(matches[1])
	if timeStr == "" {
		return nil
	}

	t, err := timeparse.ParseResetTime(timeStr)
	if err != nil {
		return nil
	}
	return &t
}
