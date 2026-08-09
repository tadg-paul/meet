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
}

// ActiveAt reports whether now falls within an occurrence's active window
// [start-Lead, start+Duration] for the recurrence anchored at anchor.
func (r Recurrence) ActiveAt(anchor, now time.Time) bool {
	switch r.Kind {
	case RecurWeekly:
		return r.weeklyActive(anchor, now)
	case RecurMonthly:
		return r.monthlyActive(anchor, now)
	}
	return false
}

func (r Recurrence) weeklyActive(anchor, now time.Time) bool {
	weeks := r.IntervalWeeks
	if weeks < 1 {
		weeks = 1
	}
	interval := time.Duration(weeks) * 7 * 24 * time.Hour
	if now.Before(anchor.Add(-r.Lead)) {
		return false
	}
	k := int(now.Sub(anchor) / interval)
	if k < 0 {
		k = 0
	}
	// The occurrence containing now is k; the lead window of k+1 may also cover
	// now. Windows do not overlap for realistic durations.
	for _, cand := range []int{k, k + 1} {
		start := anchor.Add(time.Duration(cand) * interval)
		if r.inWindow(start, now) {
			return true
		}
	}
	return false
}

func (r Recurrence) monthlyActive(anchor, now time.Time) bool {
	// Check the previous, current, and next month so a window near a month
	// boundary is not missed.
	for _, delta := range []int{-1, 0, 1} {
		y, m := addMonths(now.Year(), now.Month(), delta)
		start, ok := nthWeekdayOfMonth(y, m, r.Ordinal, r.Weekday, anchor)
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
func nthWeekdayOfMonth(year int, month time.Month, ordinal int, weekday time.Weekday, anchor time.Time) (time.Time, bool) {
	first := time.Date(year, month, 1, 0, 0, 0, 0, time.UTC)
	offset := (int(weekday) - int(first.Weekday()) + 7) % 7
	day := 1 + offset + (ordinal-1)*7
	if day > daysInMonth(year, month) {
		return time.Time{}, false
	}
	return time.Date(year, month, day, anchor.Hour(), anchor.Minute(), anchor.Second(), 0, time.UTC), true
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
