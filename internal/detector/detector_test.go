package detector

import (
	"testing"
)

func newTestDetector() *Detector {
	patterns := []string{
		"rate limit",
		"rate_limit_error",
		"Claude usage limit reached",
		"429",
	}
	return NewDetector(patterns, 75)
}

// ---------------------------------------------------------------------------
// Exit code detection
// ---------------------------------------------------------------------------

func TestDetect_ExitCode0_Completed(t *testing.T) {
	d := newTestDetector()
	result := d.Detect(0, "", "")

	if result.Result != Completed {
		t.Errorf("Result = %v; want Completed", result.Result)
	}
	if result.Reason != "exit code 0" {
		t.Errorf("Reason = %q; want \"exit code 0\"", result.Reason)
	}
}

func TestDetect_RateLimitExitCode(t *testing.T) {
	d := newTestDetector()
	result := d.Detect(75, "", "")

	if result.Result != RateLimited {
		t.Errorf("Result = %v; want RateLimited", result.Result)
	}
	if result.Reason != "exit code matches rate limit code" {
		t.Errorf("Reason = %q; want rate limit exit code reason", result.Reason)
	}
}

func TestDetect_ExitCodeDisabled(t *testing.T) {
	// rateLimitExitCode = -1 disables exit code detection.
	d := NewDetector([]string{}, -1)
	result := d.Detect(75, "", "")

	if result.Result != Failed {
		t.Errorf("Result = %v; want Failed (exit code detection disabled)", result.Result)
	}
}

// ---------------------------------------------------------------------------
// Stderr pattern matching
// ---------------------------------------------------------------------------

func TestDetect_StderrPattern(t *testing.T) {
	d := newTestDetector()
	result := d.Detect(1, "", "Error: rate limit exceeded")

	if result.Result != RateLimited {
		t.Errorf("Result = %v; want RateLimited", result.Result)
	}
	if result.Reason == "" {
		t.Error("Reason should not be empty")
	}
}

func TestDetect_StderrPattern_CaseInsensitive(t *testing.T) {
	d := newTestDetector()
	result := d.Detect(1, "", "ERROR: RATE LIMIT exceeded")

	if result.Result != RateLimited {
		t.Errorf("Result = %v; want RateLimited (case insensitive)", result.Result)
	}
}

func TestDetect_StderrPattern_429(t *testing.T) {
	d := newTestDetector()
	result := d.Detect(1, "", "HTTP 429 Too Many Requests")

	if result.Result != RateLimited {
		t.Errorf("Result = %v; want RateLimited for 429", result.Result)
	}
}

func TestDetect_StderrPattern_UsageLimit(t *testing.T) {
	d := newTestDetector()
	result := d.Detect(1, "", "Claude usage limit reached")

	if result.Result != RateLimited {
		t.Errorf("Result = %v; want RateLimited", result.Result)
	}
}

// ---------------------------------------------------------------------------
// Stdout pattern matching
// ---------------------------------------------------------------------------

func TestDetect_StdoutPattern(t *testing.T) {
	d := newTestDetector()
	result := d.Detect(1, "You hit a rate limit. Please wait.", "")

	if result.Result != RateLimited {
		t.Errorf("Result = %v; want RateLimited (stdout pattern)", result.Result)
	}
}

func TestDetect_StdoutPattern_RateLimitError(t *testing.T) {
	d := newTestDetector()
	result := d.Detect(1, "rate_limit_error: too many requests", "")

	if result.Result != RateLimited {
		t.Errorf("Result = %v; want RateLimited", result.Result)
	}
}

// ---------------------------------------------------------------------------
// Unknown error classification
// ---------------------------------------------------------------------------

func TestDetect_UnknownError(t *testing.T) {
	d := newTestDetector()
	result := d.Detect(1, "some output", "some error")

	if result.Result != Failed {
		t.Errorf("Result = %v; want Failed", result.Result)
	}
	if result.Reason == "" {
		t.Error("Reason should not be empty for unknown errors")
	}
}

func TestDetect_UnknownExitCode(t *testing.T) {
	d := newTestDetector()
	result := d.Detect(42, "", "segfault")

	if result.Result != Failed {
		t.Errorf("Result = %v; want Failed (unknown exit code, no pattern match)", result.Result)
	}
}

// ---------------------------------------------------------------------------
// Reset time extraction
// ---------------------------------------------------------------------------

func TestDetect_ResetTimeExtracted(t *testing.T) {
	d := newTestDetector()
	result := d.Detect(75, "", "Rate limit hit. Will reset at 6:30 PM.")

	if result.Result != RateLimited {
		t.Errorf("Result = %v; want RateLimited", result.Result)
	}
	if result.ResetTime == nil {
		t.Fatal("ResetTime should not be nil when reset time is in output")
	}
	if result.ResetTime.Hour() != 18 || result.ResetTime.Minute() != 30 {
		t.Errorf("ResetTime = %s; want 18:30", result.ResetTime)
	}
}

func TestDetect_NoResetTime(t *testing.T) {
	d := newTestDetector()
	result := d.Detect(75, "", "rate limited, no time info")

	if result.ResetTime != nil {
		t.Errorf("ResetTime = %s; want nil (no time in output)", result.ResetTime)
	}
}

func TestDetect_ResetTimeInStdout(t *testing.T) {
	d := newTestDetector()
	result := d.Detect(1, "rate limit reached. reset at 3pm.", "")

	if result.Result != RateLimited {
		t.Errorf("Result = %v; want RateLimited", result.Result)
	}
	if result.ResetTime == nil {
		t.Error("ResetTime should be extracted from stdout")
	}
}

// ---------------------------------------------------------------------------
// DetectionResult.String()
// ---------------------------------------------------------------------------

func TestDetectionResult_String(t *testing.T) {
	tests := []struct {
		dr   DetectionResult
		want string
	}{
		{Completed, "completed"},
		{RateLimited, "rate_limited"},
		{Failed, "failed"},
		{Unknown, "unknown"},
	}

	for _, tt := range tests {
		got := tt.dr.String()
		if got != tt.want {
			t.Errorf("%d.String() = %q; want %q", tt.dr, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// Stderr has priority over stdout
// ---------------------------------------------------------------------------

func TestDetect_StderrHasPriorityOverStdout(t *testing.T) {
	d := newTestDetector()
	// Both stderr and stdout have patterns, but stderr should be checked first.
	result := d.Detect(1, "rate limit in stdout", "rate limit in stderr")

	if result.Result != RateLimited {
		t.Errorf("Result = %v; want RateLimited", result.Result)
	}
	// The reason should mention stderr.
	if result.Reason == "" {
		t.Error("Reason should not be empty")
	}
}
