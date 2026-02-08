package resume

import (
	"fmt"
	"strings"
)

// ResumeStrategy determines how a rate-limited session should be continued.
type ResumeStrategy int

const (
	// NativeResume uses the CLI's built-in --resume flag with the session ID.
	NativeResume ResumeStrategy = iota
	// RePrompt re-sends the prompt with context about the previous attempt.
	RePrompt
)

// String returns a human-readable label for the strategy.
func (s ResumeStrategy) String() string {
	switch s {
	case NativeResume:
		return "native_resume"
	case RePrompt:
		return "re_prompt"
	default:
		return "unknown"
	}
}

// DetermineStrategy selects the best resume approach based on available
// capabilities. Native resume is preferred when the CLI supports it and a
// session ID was captured.
func DetermineStrategy(hasSessionID bool, supportsResume bool) ResumeStrategy {
	if supportsResume && hasSessionID {
		return NativeResume
	}
	return RePrompt
}

// BuildResumePrompt constructs a prompt that instructs Claude to continue from
// where a previous rate-limited session left off. The lastMessages slice
// contains recent output lines from the interrupted session (up to the last 20
// lines are included for context).
func BuildResumePrompt(attempt int, lastMessages []string, originalPrompt string) string {
	// Take at most the last 20 lines for context.
	contextLines := lastMessages
	if len(contextLines) > 20 {
		contextLines = contextLines[len(contextLines)-20:]
	}

	lastOutput := strings.Join(contextLines, "\n")

	return fmt.Sprintf(
		"[RESUMED — attempt %d. Previous session expired.\n"+
			"Last output before interruption: %s.\n"+
			"Continue from where you left off. Do not redo completed work.]\n\n"+
			"Original task:\n%s",
		attempt, lastOutput, originalPrompt,
	)
}
