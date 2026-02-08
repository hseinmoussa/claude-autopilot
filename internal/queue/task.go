package queue

import "time"

// Valid task status values.
const (
	StatusPending   = "pending"
	StatusRunning   = "running"
	StatusWaiting   = "waiting"
	StatusDone      = "done"
	StatusFailed    = "failed"
	StatusCancelled = "cancelled"
)

// Task defines a unit of work to be executed by the autopilot runner.
type Task struct {
	ID              string    `yaml:"id,omitempty"      json:"id"`
	Title           string    `yaml:"title,omitempty"   json:"title"`
	Priority        int       `yaml:"priority,omitempty" json:"priority"`
	CreatedAt       time.Time `yaml:"created_at,omitempty" json:"created_at"`
	WorkingDir      string    `yaml:"working_dir"       json:"working_dir"`
	SkipPermissions bool      `yaml:"skip_permissions,omitempty" json:"skip_permissions,omitempty"`
	Prompt          string    `yaml:"prompt"            json:"prompt"`
	ContextFiles    []string  `yaml:"context_files,omitempty" json:"context_files,omitempty"`
	Model           string    `yaml:"model,omitempty"   json:"model,omitempty"`
	MaxRetries      int       `yaml:"max_retries,omitempty" json:"max_retries"`
	EstimatedTokens int       `yaml:"estimated_tokens,omitempty" json:"estimated_tokens,omitempty"`
	Flags           []string  `yaml:"flags,omitempty"   json:"flags,omitempty"`
	Source          string    `yaml:"-"                 json:"source,omitempty"`
}

// TaskState holds the mutable runtime state for a task. It is stored separately
// from the task definition so that task YAML files remain user-editable.
type TaskState struct {
	ID                 string     `json:"id"`
	Status             string     `json:"status"`
	Attempt            int        `json:"attempt"`
	StartedAt          *time.Time `json:"started_at,omitempty"`
	EndedAt            *time.Time `json:"ended_at,omitempty"`
	LastRateLimitedAt  *time.Time `json:"last_rate_limited_at,omitempty"`
	ResumeAt           *time.Time `json:"resume_at,omitempty"`
	PromptHash         string     `json:"prompt_hash,omitempty"`
	GitCommit          string     `json:"git_commit,omitempty"`
	SessionID          string     `json:"session_id,omitempty"`
	LastNDJSONMessages []string   `json:"last_ndjson_messages,omitempty"`
}

// TaskInit is the immutable record created once per task to anchor its identity
// and creation time. Written with AtomicCreate (hardlink) to prevent races.
type TaskInit struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
}

// validTransitions defines the allowed state machine transitions.
// Map key is the source status, value is the set of allowed destination statuses.
var validTransitions = map[string]map[string]bool{
	StatusPending: {
		StatusRunning:   true,
		StatusCancelled: true,
	},
	StatusRunning: {
		StatusDone:      true,
		StatusFailed:    true,
		StatusWaiting:   true,
		StatusCancelled: true,
	},
	StatusWaiting: {
		StatusRunning:   true,
		StatusCancelled: true,
	},
	StatusFailed: {
		StatusPending:   true, // retry
		StatusCancelled: true,
	},
	StatusDone: {
		// terminal state - no transitions out
	},
	StatusCancelled: {
		StatusPending: true, // re-queue
	},
}

// ValidTransition reports whether a state transition from -> to is allowed
// by the task state machine.
func ValidTransition(from, to string) bool {
	targets, ok := validTransitions[from]
	if !ok {
		return false
	}
	return targets[to]
}
