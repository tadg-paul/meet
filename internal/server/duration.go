// ABOUTME: Compound duration parsing for meeting-room schedules (issue #17):
// ABOUTME: values like "4:30h", "90:00 min", or "45s".

package server

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ParseDuration parses a compound duration such as "4:30h" (4h30m),
// "90:00 min" (90m), "45s", or "2h". The value is an integer or an "A:B" pair,
// followed by a unit suffix h/hour, m/min, or s/sec. A colon splits the value
// into that unit and the next-smaller one (h->minutes, m->seconds); seconds
// take no colon. The minor field must be 0..59 and the result must be positive.
func ParseDuration(s string) (time.Duration, error) {
	lower := strings.ToLower(strings.TrimSpace(s))
	if lower == "" {
		return 0, fmt.Errorf("empty duration")
	}

	var suffix string
	var unit, sub time.Duration
	switch {
	case strings.HasSuffix(lower, "hour"):
		suffix, unit, sub = "hour", time.Hour, time.Minute
	case strings.HasSuffix(lower, "min"):
		suffix, unit, sub = "min", time.Minute, time.Second
	case strings.HasSuffix(lower, "sec"):
		suffix, unit, sub = "sec", time.Second, 0
	case strings.HasSuffix(lower, "h"):
		suffix, unit, sub = "h", time.Hour, time.Minute
	case strings.HasSuffix(lower, "m"):
		suffix, unit, sub = "m", time.Minute, time.Second
	case strings.HasSuffix(lower, "s"):
		suffix, unit, sub = "s", time.Second, 0
	default:
		return 0, fmt.Errorf("duration %q has no unit (expected h/hour, m/min, s/sec)", s)
	}

	value := strings.TrimSpace(lower[:len(lower)-len(suffix)])
	if value == "" {
		return 0, fmt.Errorf("duration %q has no value", s)
	}

	major, minor, hasMinor, err := splitDurationValue(value)
	if err != nil {
		return 0, fmt.Errorf("duration %q: %w", s, err)
	}
	if hasMinor {
		if sub == 0 {
			return 0, fmt.Errorf("duration %q: seconds take no colon", s)
		}
		if minor < 0 || minor >= 60 {
			return 0, fmt.Errorf("duration %q: minor field must be 0..59", s)
		}
	}

	total := time.Duration(major)*unit + time.Duration(minor)*sub
	if total <= 0 {
		return 0, fmt.Errorf("duration %q must be positive", s)
	}
	return total, nil
}

func splitDurationValue(value string) (major, minor int, hasMinor bool, err error) {
	if i := strings.IndexByte(value, ':'); i >= 0 {
		major, err = strconv.Atoi(value[:i])
		if err != nil {
			return 0, 0, false, fmt.Errorf("invalid value")
		}
		minor, err = strconv.Atoi(value[i+1:])
		if err != nil {
			return 0, 0, false, fmt.Errorf("invalid value")
		}
		return major, minor, true, nil
	}
	major, err = strconv.Atoi(value)
	if err != nil {
		return 0, 0, false, fmt.Errorf("invalid value")
	}
	return major, 0, false, nil
}
