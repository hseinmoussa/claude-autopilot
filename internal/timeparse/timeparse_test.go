package timeparse

import (
	"strings"
	"testing"
	"time"
)

func TestParseResetTime_12HrWithMinutes(t *testing.T) {
	result, err := ParseResetTime("6:30 PM")
	if err != nil {
		t.Fatalf("ParseResetTime(\"6:30 PM\") error: %v", err)
	}
	if result.Hour() != 18 || result.Minute() != 30 {
		t.Errorf("expected 18:30, got %02d:%02d", result.Hour(), result.Minute())
	}
}

func TestParseResetTime_Abbreviated(t *testing.T) {
	result, err := ParseResetTime("resets 6pm")
	if err != nil {
		t.Fatalf("ParseResetTime(\"resets 6pm\") error: %v", err)
	}
	if result.Hour() != 18 || result.Minute() != 0 {
		t.Errorf("expected 18:00, got %02d:%02d", result.Hour(), result.Minute())
	}
}

func TestParseResetTime_WithDate(t *testing.T) {
	// Use a future date to avoid "in the past" error.
	// We'll construct a date string using a month that is in the future.
	now := time.Now()
	futureMonth := now.Month() + 2
	if futureMonth > 12 {
		futureMonth -= 12
	}
	// Pick a day that exists in all months.
	input := futureMonth.String()[:3] + " 7, 1am"
	result, err := ParseResetTime("reset at " + input)
	if err != nil {
		t.Fatalf("ParseResetTime(\"reset at %s\") error: %v", input, err)
	}
	if result.Hour() != 1 || result.Minute() != 0 {
		t.Errorf("expected 01:00, got %02d:%02d", result.Hour(), result.Minute())
	}
	if result.Day() != 7 {
		t.Errorf("expected day 7, got %d", result.Day())
	}
}

func TestParseResetTime_WithTimezone(t *testing.T) {
	result, err := ParseResetTime("3pm (America/Santiago)")
	if err != nil {
		t.Fatalf("ParseResetTime with timezone error: %v", err)
	}
	loc, _ := time.LoadLocation("America/Santiago")
	if result.Location().String() != loc.String() {
		t.Errorf("location = %s; want %s", result.Location(), loc)
	}
	if result.Hour() != 15 {
		t.Errorf("hour = %d; want 15", result.Hour())
	}
}

func TestParseResetTime_24Hr(t *testing.T) {
	result, err := ParseResetTime("14:30")
	if err != nil {
		t.Fatalf("ParseResetTime(\"14:30\") error: %v", err)
	}
	if result.Hour() != 14 || result.Minute() != 30 {
		t.Errorf("expected 14:30, got %02d:%02d", result.Hour(), result.Minute())
	}
}

func TestParseResetTime_GarbageInput(t *testing.T) {
	_, err := ParseResetTime("no time here whatsoever")
	if err == nil {
		t.Fatal("expected error for garbage input, got nil")
	}
}

func TestParseResetTime_Empty(t *testing.T) {
	_, err := ParseResetTime("")
	if err == nil {
		t.Fatal("expected error for empty string, got nil")
	}
}

func TestParseResetTime_TimeOnlyInPast_RollsForward(t *testing.T) {
	// Build a time string that is definitely in the past today.
	// Use 1 minute ago's time in 12hr format.
	pastTime := time.Now().Add(-1 * time.Minute)
	hour := pastTime.Hour()
	minute := pastTime.Minute()

	ampm := "am"
	h12 := hour
	if hour >= 12 {
		ampm = "pm"
		if hour > 12 {
			h12 = hour - 12
		}
	}
	if h12 == 0 {
		h12 = 12
	}

	input := ""
	if minute > 0 {
		input = time.Now().Add(-1 * time.Minute).Format("3:04 PM")
	} else {
		input = time.Now().Add(-1 * time.Minute).Format("3:04 PM")
	}
	_ = ampm
	_ = h12

	result, err := ParseResetTime(input)
	if err != nil {
		t.Fatalf("ParseResetTime(%q) error: %v", input, err)
	}

	// The result should be in the future (rolled forward by 24h).
	if !result.After(time.Now()) {
		t.Errorf("expected future time, got %s (now: %s)", result, time.Now())
	}
}

func TestParseResetTime_12AM(t *testing.T) {
	result, err := ParseResetTime("12:00 AM")
	if err != nil {
		t.Fatalf("ParseResetTime(\"12:00 AM\") error: %v", err)
	}
	if result.Hour() != 0 || result.Minute() != 0 {
		t.Errorf("12:00 AM should be 00:00, got %02d:%02d", result.Hour(), result.Minute())
	}
}

func TestParseResetTime_12PM(t *testing.T) {
	result, err := ParseResetTime("12:00 PM")
	if err != nil {
		t.Fatalf("ParseResetTime(\"12:00 PM\") error: %v", err)
	}
	if result.Hour() != 12 || result.Minute() != 0 {
		t.Errorf("12:00 PM should be 12:00, got %02d:%02d", result.Hour(), result.Minute())
	}
}

func TestParseResetTime_InvalidTimezone(t *testing.T) {
	// Invalid timezone should fall back to local.
	result, err := ParseResetTime("3pm (Fake/Timezone)")
	if err != nil {
		t.Fatalf("ParseResetTime with invalid tz error: %v", err)
	}
	// Should still parse the time, using local timezone.
	if result.Hour() != 15 {
		t.Errorf("hour = %d; want 15", result.Hour())
	}
}

func TestParseResetTime_DateInPastReturnsError(t *testing.T) {
	// Explicit date in the past should return an error so caller can back off.
	past := time.Now().AddDate(0, -1, 0)
	input := past.Format("Jan 2, 3pm")
	_, err := ParseResetTime("reset at " + input)
	if err == nil {
		t.Fatal("expected error for explicit past date")
	}
}

func TestParseResetTime_MonthBoundaryFormatAccepted(t *testing.T) {
	// Ensure month/day format parses at all (boundary behavior is date-aware).
	_, err := ParseResetTime("Dec 31, 11pm")
	// Either future parse or explicit past-date error is acceptable depending on current date.
	if err != nil && !strings.Contains(err.Error(), "in the past") {
		t.Fatalf("unexpected error for month-boundary format: %v", err)
	}
}
