// ABOUTME: Recurring meeting-room schedules (issue #17): weekly / every-N-weeks
// ABOUTME: and monthly Nth-weekday occurrence computation, UTC only.

package server

import (
	"time"
)

// RecurKind is the kind of recurrence a registry row describes.
type RecurKind string

const (
	// RecurWeekly repeats every IntervalWeeks weeks from the row's anchor
	// (valid_from), keeping the anchor's weekday and time-of-day.
	RecurWeekly RecurKind = "weekly"
	// RecurMonthly repeats on the Ordinal-th Weekday of each month at the
	// anchor's time-of-day.
	RecurMonthly RecurKind = "monthly"
)

// Recurrence describes a repeating schedule attached to a created registry row.
// The row's ValidFrom is the anchor: for weekly it is the first occurrence
// (weekday and time-of-day); for monthly it is the series start and supplies
// the time-of-day. All times are UTC.
type Recurrence struct {
	Kind          RecurKind
	IntervalWeeks int          // weekly: >= 1 (fortnightly = 2)
	Ordinal       int          // monthly: 1..5
	Weekday       time.Weekday // monthly
	Duration      time.Duration
	Lead          time.Duration
	Ends          time.Time // zero means no end; otherwise inclusive last instant
	// Tz is an IANA timezone name (e.g. "Europe/Dublin"). Empty means UTC.
	// When set, occurrence start times keep their local wall-clock across DST,
	// so the UTC instant follows the zone's offset changes (#18).
	Tz string
}

// location resolves the recurrence's timezone, falling back to UTC when unset
// or unresolvable (create-time validation rejects bad names before storage).
func (r Recurrence) location() *time.Location {
	if r.Tz == "" {
		return time.UTC
	}
	if loc, err := time.LoadLocation(r.Tz); err == nil {
		return loc
	}
	return time.UTC
}

// ActiveAt reports whether now falls within an occurrence's active window
// [start-Lead, start+Duration] for the recurrence anchored at anchor.
func (r Recurrence) ActiveAt(anchor, now time.Time) bool {
	loc := r.location()
	switch r.Kind {
	case RecurWeekly:
		return r.weeklyActive(anchor, now, loc)
	case RecurMonthly:
		return r.monthlyActive(anchor, now, loc)
	}
	return false
}

func (r Recurrence) weeklyActive(anchor, now time.Time, loc *time.Location) bool {
	weeks := r.IntervalWeeks
	if weeks < 1 {
		weeks = 1
	}
	al := anchor.In(loc)
	// Occurrence 0 is the anchor's local wall-clock; later occurrences keep that
	// wall-clock every N weeks (calendar addition, so DST is followed).
	start0 := time.Date(al.Year(), al.Month(), al.Day(), al.Hour(), al.Minute(), al.Second(), 0, loc)
	k := int(now.Sub(start0).Hours()) / (24 * 7 * weeks)
	// Check a small band around the estimate to absorb rounding and DST.
	for _, cand := range []int{k - 1, k, k + 1} {
		if cand < 0 {
			continue
		}
		start := start0.AddDate(0, 0, 7*weeks*cand)
		if r.inWindow(start, now) {
			return true
		}
	}
	return false
}

func (r Recurrence) monthlyActive(anchor, now time.Time, loc *time.Location) bool {
	al := anchor.In(loc)
	nl := now.In(loc)
	// Check the previous, current, and next month so a window near a month
	// boundary is not missed.
	for _, delta := range []int{-1, 0, 1} {
		y, m := addMonths(nl.Year(), nl.Month(), delta)
		start, ok := nthWeekdayOfMonth(y, m, r.Ordinal, r.Weekday, al, loc)
		if !ok {
			continue // this month has no Nth instance of the weekday
		}
		if start.Before(anchor) {
			continue // before the series start
		}
		if r.inWindow(start, now) {
			return true
		}
	}
	return false
}

// inWindow reports whether now is within [start-Lead, start+Duration] and the
// occurrence start is not after the series end.
func (r Recurrence) inWindow(start, now time.Time) bool {
	if !r.Ends.IsZero() && start.After(r.Ends) {
		return false
	}
	return !now.Before(start.Add(-r.Lead)) && !now.After(start.Add(r.Duration))
}

// nthWeekdayOfMonth returns the UTC instant of the ordinal-th weekday of the
// given month at the anchor's time-of-day, and false when that month has no
// such instance (e.g. no fifth Monday).
func nthWeekdayOfMonth(year int, month time.Month, ordinal int, weekday time.Weekday, anchorLocal time.Time, loc *time.Location) (time.Time, bool) {
	first := time.Date(year, month, 1, 0, 0, 0, 0, loc)
	offset := (int(weekday) - int(first.Weekday()) + 7) % 7
	day := 1 + offset + (ordinal-1)*7
	if day > daysInMonth(year, month) {
		return time.Time{}, false
	}
	return time.Date(year, month, day, anchorLocal.Hour(), anchorLocal.Minute(), anchorLocal.Second(), 0, loc), true
}

func daysInMonth(year int, month time.Month) int {
	// Day 0 of the next month is the last day of this month.
	return time.Date(year, month+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

func addMonths(year int, month time.Month, delta int) (int, time.Month) {
	m := int(month) - 1 + delta
	year += m / 12
	m %= 12
	if m < 0 {
		m += 12
		year--
	}
	return year, time.Month(m + 1)
}
