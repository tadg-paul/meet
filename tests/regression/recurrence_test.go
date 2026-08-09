// ABOUTME: Regression tests for recurring meeting-room schedules (issue #17).
// ABOUTME: Covers the duration parser and (later) the recurrence occurrence gate.

package regression

import (
	"testing"
	"time"

	"github.com/tigger-developer/meet/internal/server"
)

// --- AC17.11: duration parsing ---

func TestParseDuration_ColonHoursMinutes_RT17_34(t *testing.T) {
	got, err := server.ParseDuration("4:30h")
	if err != nil {
		t.Fatalf("ParseDuration(4:30h): %v", err)
	}
	if want := 4*time.Hour + 30*time.Minute; got != want {
		t.Errorf("ParseDuration(4:30h) = %v, want %v", got, want)
	}
}

func TestParseDuration_ColonMinutesSeconds_RT17_35(t *testing.T) {
	got, err := server.ParseDuration("90:00 min")
	if err != nil {
		t.Fatalf("ParseDuration(90:00 min): %v", err)
	}
	if want := 90 * time.Minute; got != want {
		t.Errorf("ParseDuration(90:00 min) = %v, want %v", got, want)
	}
}

func TestParseDuration_PlainUnitsAndSynonyms_RT17_36(t *testing.T) {
	cases := map[string]time.Duration{
		"4h":     4 * time.Hour,
		"4hour":  4 * time.Hour,
		"30m":    30 * time.Minute,
		"30min":  30 * time.Minute,
		"45s":    45 * time.Second,
		"45sec":  45 * time.Second,
		"1:30m":  1*time.Minute + 30*time.Second,
		"2:15 h": 2*time.Hour + 15*time.Minute,
	}
	for in, want := range cases {
		got, err := server.ParseDuration(in)
		if err != nil {
			t.Errorf("ParseDuration(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("ParseDuration(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestParseDuration_Rejects_RT17_37(t *testing.T) {
	for _, in := range []string{"5", "abc", "0h", "-3h", "", "h", "4x"} {
		if got, err := server.ParseDuration(in); err == nil {
			t.Errorf("ParseDuration(%q) = %v, want error", in, got)
		}
	}
}

func TestParseDuration_RejectsMinorOverflow_RT17_38(t *testing.T) {
	for _, in := range []string{"4:70h", "1:60m"} {
		if got, err := server.ParseDuration(in); err == nil {
			t.Errorf("ParseDuration(%q) = %v, want error (minor field >= 60)", in, got)
		}
	}
}
