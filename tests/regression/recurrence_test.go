// ABOUTME: Regression tests for recurring meeting-room schedules (issue #17).
// ABOUTME: Covers the duration parser and (later) the recurrence occurrence gate.

package regression

import (
	"net/http"
	"testing"
	"time"

	"github.com/tigger-developer/meet/internal/server"
)

// --- recurrence gate helpers (reuse the #7 gate fixture) ---

func (f *fixture) registerRecurring(t *testing.T, room string, anchor time.Time, rec server.Recurrence) {
	t.Helper()
	r := rec
	if err := f.rooms.Append(server.RoomLogEntry{
		Timestamp:  f.now,
		Room:       room,
		Status:     server.RoomCreated,
		ValidFrom:  anchor,
		Recurrence: &r,
	}); err != nil {
		t.Fatalf("register recurring %s: %v", room, err)
	}
}

func (f *fixture) admitted(t *testing.T, room string) bool {
	t.Helper()
	status, body := f.get(t, "/"+room)
	return status == http.StatusOK && bodyIsMeetingPage(body)
}

func weeklyRec(weeks int) server.Recurrence {
	return server.Recurrence{
		Kind:          server.RecurWeekly,
		IntervalWeeks: weeks,
		Duration:      4 * time.Hour,
		Lead:          15 * time.Minute,
	}
}

func monthlyRec(ordinal int, wd time.Weekday) server.Recurrence {
	return server.Recurrence{
		Kind:     server.RecurMonthly,
		Ordinal:  ordinal,
		Weekday:  wd,
		Duration: 4 * time.Hour,
		Lead:     15 * time.Minute,
	}
}

// --- AC17.1: recurring definitions stored, last-row-wins ---

func TestRecur_StoredWeekly_RT17_1(t *testing.T) {
	f := newGateFixture(t)
	f.registerRecurring(t, "wk", mustRFC3339(t, "2026-08-11T19:00:00Z"), weeklyRec(1))
	e := f.rooms.LatestByRoom("wk")
	if e == nil || e.Recurrence == nil || e.Recurrence.Kind != server.RecurWeekly {
		t.Fatalf("weekly recurrence not read back: %+v", e)
	}
}

func TestRecur_StoredMonthly_RT17_2(t *testing.T) {
	f := newGateFixture(t)
	f.registerRecurring(t, "mo", mustRFC3339(t, "2026-08-01T18:00:00Z"), monthlyRec(1, time.Wednesday))
	e := f.rooms.LatestByRoom("mo")
	if e == nil || e.Recurrence == nil || e.Recurrence.Kind != server.RecurMonthly || e.Recurrence.Weekday != time.Wednesday {
		t.Fatalf("monthly recurrence not read back: %+v", e)
	}
}

func TestRecur_LaterDefinitionSupersedes_RT17_3(t *testing.T) {
	f := newGateFixture(t)
	a1 := mustRFC3339(t, "2026-08-11T19:00:00Z")
	a2 := mustRFC3339(t, "2026-08-12T10:00:00Z")
	f.registerRecurring(t, "x", a1, weeklyRec(1))
	f.registerRecurring(t, "x", a2, weeklyRec(1))
	// The latest anchor wins: active at a2, not at a1's time.
	f.now = a2
	if !f.admitted(t, "x") {
		t.Error("latest definition (a2) should be active")
	}
	f.now = a1
	if f.admitted(t, "x") {
		t.Error("superseded definition (a1) should not be active")
	}
}

func TestRecur_CancellationSupersedes_RT17_4(t *testing.T) {
	f := newGateFixture(t)
	a := mustRFC3339(t, "2026-08-11T19:00:00Z")
	f.registerRecurring(t, "x", a, weeklyRec(1))
	f.cancelRoom(t, "x")
	if e := f.rooms.LatestByRoom("x"); e == nil || e.Status != server.RoomCancelled {
		t.Fatalf("cancellation should supersede: %+v", e)
	}
}

// --- AC17.2: weekly / every-N-weeks occurrence gate ---

func TestRecur_WeeklyFirstOccurrence_RT17_5(t *testing.T) {
	f := newGateFixture(t)
	a := mustRFC3339(t, "2026-08-11T19:00:00Z")
	f.registerRecurring(t, "wk", a, weeklyRec(1))
	f.now = a
	if !f.admitted(t, "wk") {
		t.Error("weekly first occurrence should admit")
	}
}

func TestRecur_WeeklyBetween_RT17_6(t *testing.T) {
	f := newGateFixture(t)
	a := mustRFC3339(t, "2026-08-11T19:00:00Z")
	f.registerRecurring(t, "wk", a, weeklyRec(1))
	f.now = a.Add(3 * 24 * time.Hour)
	if f.admitted(t, "wk") {
		t.Error("weekly between occurrences should refuse")
	}
}

func TestRecur_WeeklyLaterOccurrence_RT17_7(t *testing.T) {
	f := newGateFixture(t)
	a := mustRFC3339(t, "2026-08-11T19:00:00Z")
	f.registerRecurring(t, "wk", a, weeklyRec(1))
	f.now = a.Add(7 * 24 * time.Hour)
	if !f.admitted(t, "wk") {
		t.Error("weekly later occurrence should admit")
	}
}

func TestRecur_Fortnightly_RT17_8(t *testing.T) {
	f := newGateFixture(t)
	a := mustRFC3339(t, "2026-08-11T19:00:00Z")
	f.registerRecurring(t, "ft", a, weeklyRec(2))
	f.now = a.Add(7 * 24 * time.Hour)
	if f.admitted(t, "ft") {
		t.Error("fortnightly skipped week should refuse")
	}
	f.now = a.Add(14 * 24 * time.Hour)
	if !f.admitted(t, "ft") {
		t.Error("fortnightly following occurrence should admit")
	}
}

// --- AC17.3: monthly Nth-weekday occurrence gate ---

func TestRecur_MonthlyFirstWednesday_RT17_9(t *testing.T) {
	f := newGateFixture(t)
	f.registerRecurring(t, "mo", mustRFC3339(t, "2026-08-01T18:00:00Z"), monthlyRec(1, time.Wednesday))
	f.now = mustRFC3339(t, "2026-09-02T18:00:00Z") // first Wednesday of Sept 2026
	if !f.admitted(t, "mo") {
		t.Error("first Wednesday occurrence should admit")
	}
}

func TestRecur_MonthlySecondTuesday_RT17_10(t *testing.T) {
	f := newGateFixture(t)
	f.registerRecurring(t, "mo", mustRFC3339(t, "2026-08-01T20:30:00Z"), monthlyRec(2, time.Tuesday))
	f.now = mustRFC3339(t, "2026-09-08T20:30:00Z") // second Tuesday of Sept 2026
	if !f.admitted(t, "mo") {
		t.Error("second Tuesday occurrence should admit")
	}
}

func TestRecur_MonthlyAbsentNthWeekday_RT17_11(t *testing.T) {
	f := newGateFixture(t)
	// Feb 2026 has only four Mondays, so a "fifth Monday" has no occurrence.
	f.registerRecurring(t, "mo", mustRFC3339(t, "2026-01-01T18:00:00Z"), monthlyRec(5, time.Monday))
	f.now = mustRFC3339(t, "2026-02-16T18:00:00Z")
	if f.admitted(t, "mo") {
		t.Error("absent fifth Monday should refuse")
	}
}

func TestRecur_MonthlyAdjacentMonth_RT17_12(t *testing.T) {
	f := newGateFixture(t)
	f.registerRecurring(t, "mo", mustRFC3339(t, "2026-08-01T18:00:00Z"), monthlyRec(1, time.Wednesday))
	f.now = mustRFC3339(t, "2026-10-07T18:00:00Z") // first Wednesday of Oct 2026
	if !f.admitted(t, "mo") {
		t.Error("adjacent-month occurrence should admit at the correct date")
	}
}

// --- AC17.5: window boundaries ---

func TestRecur_BoundaryStartLead_RT17_17(t *testing.T) {
	f := newGateFixture(t)
	a := mustRFC3339(t, "2026-08-11T19:00:00Z")
	f.registerRecurring(t, "wk", a, weeklyRec(1))
	f.now = a.Add(-15 * time.Minute)
	if !f.admitted(t, "wk") {
		t.Error("at start-lead should admit")
	}
}

func TestRecur_BoundaryBeforeLead_RT17_18(t *testing.T) {
	f := newGateFixture(t)
	a := mustRFC3339(t, "2026-08-11T19:00:00Z")
	f.registerRecurring(t, "wk", a, weeklyRec(1))
	f.now = a.Add(-15*time.Minute - time.Second)
	if f.admitted(t, "wk") {
		t.Error("one second before start-lead should refuse")
	}
}

func TestRecur_BoundaryEnd_RT17_19(t *testing.T) {
	f := newGateFixture(t)
	a := mustRFC3339(t, "2026-08-11T19:00:00Z")
	f.registerRecurring(t, "wk", a, weeklyRec(1))
	f.now = a.Add(4 * time.Hour)
	if !f.admitted(t, "wk") {
		t.Error("at start+duration should admit")
	}
}

func TestRecur_BoundaryAfterEnd_RT17_20(t *testing.T) {
	f := newGateFixture(t)
	a := mustRFC3339(t, "2026-08-11T19:00:00Z")
	f.registerRecurring(t, "wk", a, weeklyRec(1))
	f.now = a.Add(4*time.Hour + time.Second)
	if f.admitted(t, "wk") {
		t.Error("one second after start+duration should refuse")
	}
}

// --- AC17.6: series end ---

func TestRecur_BeforeSeriesEnd_RT17_21(t *testing.T) {
	f := newGateFixture(t)
	a := mustRFC3339(t, "2026-08-11T19:00:00Z")
	rec := weeklyRec(1)
	rec.Ends = a.Add(10 * 24 * time.Hour)
	f.registerRecurring(t, "wk", a, rec)
	f.now = a.Add(7 * 24 * time.Hour)
	if !f.admitted(t, "wk") {
		t.Error("occurrence before series end should admit")
	}
}

func TestRecur_AfterSeriesEnd_RT17_22(t *testing.T) {
	f := newGateFixture(t)
	a := mustRFC3339(t, "2026-08-11T19:00:00Z")
	rec := weeklyRec(1)
	rec.Ends = a.Add(10 * 24 * time.Hour)
	f.registerRecurring(t, "wk", a, rec)
	f.now = a.Add(14 * 24 * time.Hour)
	if f.admitted(t, "wk") {
		t.Error("occurrence after series end should refuse")
	}
}

func TestRecur_NoSeriesEnd_RT17_23(t *testing.T) {
	f := newGateFixture(t)
	a := mustRFC3339(t, "2026-08-11T19:00:00Z")
	f.registerRecurring(t, "wk", a, weeklyRec(1))
	f.now = a.Add(14 * 24 * time.Hour)
	if !f.admitted(t, "wk") {
		t.Error("with no series end, a far occurrence should admit")
	}
}

// --- AC17.7: cancellation ---

func TestRecur_CancelCloses_RT17_24(t *testing.T) {
	f := newGateFixture(t)
	a := mustRFC3339(t, "2026-08-11T19:00:00Z")
	f.registerRecurring(t, "wk", a, weeklyRec(1))
	f.cancelRoom(t, "wk")
	f.now = a
	if f.admitted(t, "wk") {
		t.Error("cancelled recurring room should refuse during occurrence")
	}
}

func TestRecur_RecreateReopens_RT17_25(t *testing.T) {
	f := newGateFixture(t)
	a := mustRFC3339(t, "2026-08-11T19:00:00Z")
	f.registerRecurring(t, "wk", a, weeklyRec(1))
	f.cancelRoom(t, "wk")
	f.registerRecurring(t, "wk", a, weeklyRec(1))
	f.now = a
	if !f.admitted(t, "wk") {
		t.Error("recreated recurring room should admit during occurrence")
	}
}

// --- AC17.9: one-off rooms unchanged ---

func TestRecur_OneOffInside_RT17_30(t *testing.T) {
	f := newGateFixture(t)
	a := mustRFC3339(t, "2026-08-11T19:00:00Z")
	f.registerRoom(t, "one", a, a.Add(2*time.Hour))
	f.now = a.Add(time.Hour)
	if !f.admitted(t, "one") {
		t.Error("one-off inside its window should admit")
	}
}

func TestRecur_OneOffOutside_RT17_31(t *testing.T) {
	f := newGateFixture(t)
	a := mustRFC3339(t, "2026-08-11T19:00:00Z")
	f.registerRoom(t, "one", a, a.Add(2*time.Hour))
	f.now = a.Add(3 * time.Hour)
	if f.admitted(t, "one") {
		t.Error("one-off outside its window should refuse")
	}
}

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

// --- #19: NextOccurrences ---

func TestNext_Weekly_RT19_1(t *testing.T) {
	a := mustRFC3339(t, "2026-08-11T19:00:00Z")
	r := weeklyRec(1)
	got := r.NextOccurrences(a, mustRFC3339(t, "2026-08-11T00:00:00Z"), 3)
	want := []string{"2026-08-11T19:00:00Z", "2026-08-18T19:00:00Z", "2026-08-25T19:00:00Z"}
	assertOccurrences(t, got, want)
}

func TestNext_Fortnightly_RT19_2(t *testing.T) {
	a := mustRFC3339(t, "2026-08-11T19:00:00Z")
	got := weeklyRec(2).NextOccurrences(a, mustRFC3339(t, "2026-08-12T00:00:00Z"), 2)
	assertOccurrences(t, got, []string{"2026-08-25T19:00:00Z", "2026-09-08T19:00:00Z"})
}

func TestNext_Monthly_RT19_3(t *testing.T) {
	a := mustRFC3339(t, "2026-08-01T18:00:00Z")
	got := monthlyRec(2, time.Tuesday).NextOccurrences(a, mustRFC3339(t, "2026-08-15T00:00:00Z"), 2)
	// 2nd Tuesday: Sep 8, Oct 13 2026
	assertOccurrences(t, got, []string{"2026-09-08T18:00:00Z", "2026-10-13T18:00:00Z"})
}

func TestNext_RespectsEnds_RT19_4(t *testing.T) {
	a := mustRFC3339(t, "2026-08-11T19:00:00Z")
	r := weeklyRec(1)
	r.Ends = mustRFC3339(t, "2026-08-20T00:00:00Z") // only Aug 11 and Aug 18 fall before it
	got := r.NextOccurrences(a, mustRFC3339(t, "2026-08-01T00:00:00Z"), 6)
	assertOccurrences(t, got, []string{"2026-08-11T19:00:00Z", "2026-08-18T19:00:00Z"})
}

func assertOccurrences(t *testing.T, got []time.Time, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d occurrences, want %d: %v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i].UTC().Format(time.RFC3339) != w {
			t.Errorf("occurrence %d = %s, want %s", i, got[i].UTC().Format(time.RFC3339), w)
		}
	}
}
