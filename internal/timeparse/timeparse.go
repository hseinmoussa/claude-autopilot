package timeparse

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ParseResetTime parses a human-readable reset time string into a time.Time.
//
// Supported formats include:
//   - "6:30 PM"             (12hr, time only)
//   - "resets 6pm"          (abbreviated 12hr)
//   - "reset at Oct 7, 1am" (date + time)
//   - "3pm (America/Santiago)" (with explicit timezone)
//   - "14:30"               (24hr)
//
// Priority chain:
//  1. If a TZ name in parentheses is present, use that timezone.
//  2. Otherwise, use system local timezone.
//  3. If the parsed time is in the past:
//     - If an explicit date was present in the string, return an error
//       (caller should fall back to backoff).
//     - If time-only, assume today first; if still in the past, add 24h.
func ParseResetTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, fmt.Errorf("empty reset time string")
	}

	loc, s := extractTimezone(s)
	if loc == nil {
		loc = time.Now().Location()
	}

	dateMonth, dateDay, hasDate := extractDate(s)
	hour, minute, err := extractTime(s)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse reset time %q: %w", s, err)
	}

	now := time.Now().In(loc)

	var year, day int
	var month time.Month

	if hasDate {
		year = now.Year()
		month = dateMonth
		day = dateDay
	} else {
		year = now.Year()
		month = now.Month()
		day = now.Day()
	}

	result := time.Date(year, month, day, hour, minute, 0, 0, loc)

	if result.Before(now) {
		if hasDate {
			// Explicit date is in the past; caller should use backoff.
			return result, fmt.Errorf("reset time %s is in the past", result.Format(time.RFC3339))
		}
		// Time-only: roll forward 24h.
		result = result.Add(24 * time.Hour)
	}

	return result, nil
}

// reTimezone matches a timezone name in parentheses, e.g. "(America/Santiago)".
var reTimezone = regexp.MustCompile(`\(([A-Za-z_]+/[A-Za-z_]+)\)`)

// extractTimezone looks for a timezone name in parentheses and returns the
// loaded *time.Location. The timezone portion is removed from the returned
// string. Returns nil location if no timezone is found.
func extractTimezone(s string) (*time.Location, string) {
	m := reTimezone.FindStringSubmatch(s)
	if m == nil {
		return nil, s
	}
	loc, err := time.LoadLocation(m[1])
	if err != nil {
		return nil, s
	}
	cleaned := strings.Replace(s, m[0], "", 1)
	return loc, strings.TrimSpace(cleaned)
}

// monthNames maps abbreviated and full month names to time.Month.
var monthNames = map[string]time.Month{
	"jan": time.January, "january": time.January,
	"feb": time.February, "february": time.February,
	"mar": time.March, "march": time.March,
	"apr": time.April, "april": time.April,
	"may": time.May,
	"jun": time.June, "june": time.June,
	"jul": time.July, "july": time.July,
	"aug": time.August, "august": time.August,
	"sep": time.September, "september": time.September,
	"oct": time.October, "october": time.October,
	"nov": time.November, "november": time.November,
	"dec": time.December, "december": time.December,
}

// reDate matches patterns like "Oct 7", "October 7", "Oct 07".
var reDate = regexp.MustCompile(`(?i)(jan(?:uary)?|feb(?:ruary)?|mar(?:ch)?|apr(?:il)?|may|jun(?:e)?|jul(?:y)?|aug(?:ust)?|sep(?:tember)?|oct(?:ober)?|nov(?:ember)?|dec(?:ember)?)\s+(\d{1,2})`)

// extractDate looks for a month + day pattern in the string.
func extractDate(s string) (time.Month, int, bool) {
	m := reDate.FindStringSubmatch(s)
	if m == nil {
		return 0, 0, false
	}
	month, ok := monthNames[strings.ToLower(m[1])]
	if !ok {
		return 0, 0, false
	}
	day, err := strconv.Atoi(m[2])
	if err != nil {
		return 0, 0, false
	}
	return month, day, true
}

// reTime12 matches 12hr times: "6:30 PM", "6:30pm", "1am", "1 AM".
var reTime12 = regexp.MustCompile(`(?i)(\d{1,2})(?::(\d{2}))?\s*(am|pm)`)

// reTime24 matches 24hr times: "14:30", "08:00".
var reTime24 = regexp.MustCompile(`(\d{1,2}):(\d{2})`)

// extractTime parses the time component from the string.
func extractTime(s string) (hour, minute int, err error) {
	// Try 12hr format first (more specific due to am/pm).
	if m := reTime12.FindStringSubmatch(s); m != nil {
		h, _ := strconv.Atoi(m[1])
		min := 0
		if m[2] != "" {
			min, _ = strconv.Atoi(m[2])
		}
		ampm := strings.ToLower(m[3])

		if h < 1 || h > 12 {
			return 0, 0, fmt.Errorf("invalid 12hr hour: %d", h)
		}

		if ampm == "am" {
			if h == 12 {
				h = 0
			}
		} else { // pm
			if h != 12 {
				h += 12
			}
		}
		return h, min, nil
	}

	// Try 24hr format.
	if m := reTime24.FindStringSubmatch(s); m != nil {
		h, _ := strconv.Atoi(m[1])
		min, _ := strconv.Atoi(m[2])
		if h < 0 || h > 23 || min < 0 || min > 59 {
			return 0, 0, fmt.Errorf("invalid 24hr time: %02d:%02d", h, min)
		}
		return h, min, nil
	}

	return 0, 0, fmt.Errorf("no recognizable time pattern in %q", s)
}
