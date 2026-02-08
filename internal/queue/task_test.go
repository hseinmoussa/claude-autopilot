package queue

import "testing"

func TestValidTransition_AllValid(t *testing.T) {
	valid := []struct {
		from, to string
	}{
		// From pending
		{StatusPending, StatusRunning},
		{StatusPending, StatusCancelled},
		// From running
		{StatusRunning, StatusDone},
		{StatusRunning, StatusFailed},
		{StatusRunning, StatusWaiting},
		{StatusRunning, StatusCancelled},
		// From waiting
		{StatusWaiting, StatusRunning},
		{StatusWaiting, StatusCancelled},
		// From failed
		{StatusFailed, StatusPending},
		{StatusFailed, StatusCancelled},
		// From cancelled
		{StatusCancelled, StatusPending},
	}

	for _, tc := range valid {
		if !ValidTransition(tc.from, tc.to) {
			t.Errorf("ValidTransition(%q, %q) = false; want true", tc.from, tc.to)
		}
	}
}

func TestValidTransition_AllInvalid(t *testing.T) {
	invalid := []struct {
		from, to string
	}{
		// done is terminal
		{StatusDone, StatusRunning},
		{StatusDone, StatusPending},
		{StatusDone, StatusFailed},
		{StatusDone, StatusWaiting},
		{StatusDone, StatusCancelled},
		// No self transitions
		{StatusPending, StatusPending},
		{StatusRunning, StatusRunning},
		// Cannot go backwards in unexpected ways
		{StatusPending, StatusDone},
		{StatusPending, StatusFailed},
		{StatusPending, StatusWaiting},
		{StatusWaiting, StatusDone},
		{StatusWaiting, StatusFailed},
		{StatusFailed, StatusRunning},
		{StatusFailed, StatusDone},
		{StatusFailed, StatusWaiting},
		{StatusCancelled, StatusRunning},
		{StatusCancelled, StatusDone},
		{StatusCancelled, StatusFailed},
		{StatusCancelled, StatusWaiting},
		// Unknown status
		{"unknown", StatusRunning},
		{StatusRunning, "unknown"},
	}

	for _, tc := range invalid {
		if ValidTransition(tc.from, tc.to) {
			t.Errorf("ValidTransition(%q, %q) = true; want false", tc.from, tc.to)
		}
	}
}

func TestStatusConstants(t *testing.T) {
	// Verify all status constants have expected values.
	statuses := map[string]string{
		"pending":   StatusPending,
		"running":   StatusRunning,
		"waiting":   StatusWaiting,
		"done":      StatusDone,
		"failed":    StatusFailed,
		"cancelled": StatusCancelled,
	}
	for expected, got := range statuses {
		if got != expected {
			t.Errorf("Status constant = %q; want %q", got, expected)
		}
	}
}
