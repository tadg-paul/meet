// ABOUTME: Regression tests for timezone-aware recurring schedules (issue #18).
// ABOUTME: Occurrences computed in an IANA zone follow DST; fixed Dublin dates.

package regression

import (
	"strings"
	"testing"
	"time"

	"github.com/tigger-developer/meet/internal/server"
)

func dublinLoc(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Dublin")
	if err != nil {
		t.Fatalf("LoadLocation(Europe/Dublin): %v", err)
	}
	return loc
}

func weeklyTz(weeks int, tz string) server.Recurrence {
	r := weeklyRec(weeks)
	r.Tz = tz
	return r
}

// --- AC18.1: zone stored, computed in-zone, UTC default ---

func TestTz_StoredAndRead_RT18_1(t *testing.T) {
	f := newGateFixture(t)
	// 18:00Z is 19:00 Dublin in summer.
	f.registerRecurring(t, "z", mustRFC3339(t, "2026-08-11T18:00:00Z"), weeklyTz(1, "Europe/Dublin"))
	e := f.rooms.LatestByRoom("z")
	if e == nil || e.Recurrence == nil || e.Recurrence.Tz != "Europe/Dublin" {
		t.Fatalf("timezone not read back: %+v", e)
	}
}

func TestTz_AdmitDuringInZoneOccurrence_RT18_2(t *testing.T) {
	f := newGateFixture(t)
	f.registerRecurring(t, "z", mustRFC3339(t, "2026-08-11T18:00:00Z"), weeklyTz(1, "Europe/Dublin"))
	f.now = mustRFC3339(t, "2026-08-11T18:00:00Z") // 19:00 Dublin, occurrence start
	if !f.admitted(t, "z") {
		t.Error("should admit during the in-zone occurrence")
	}
}

func TestTz_EmptyZoneIsUTC_RT18_3(t *testing.T) {
	f := newGateFixture(t)
	a := mustRFC3339(t, "2026-08-11T19:00:00Z")
	f.registerRecurring(t, "u", a, weeklyTz(1, "")) // empty tz == UTC
	f.now = a
	if !f.admitted(t, "u") {
		t.Error("empty timezone should behave as UTC and admit at the anchor instant")
	}
}

// --- AC18.2: DST is observed (UTC instant shifts across the transition) ---

func TestTz_SummerOccurrenceUTCInstant_RT18_4(t *testing.T) {
	f := newGateFixture(t)
	// Anchor 18:00Z = 19:00 Dublin summer time (IST, UTC+1).
	f.registerRecurring(t, "z", mustRFC3339(t, "2026-08-11T18:00:00Z"), weeklyTz(1, "Europe/Dublin"))
	f.now = mustRFC3339(t, "2026-08-11T18:00:00Z")
	if !f.admitted(t, "z") {
		t.Error("summer occurrence should admit at 18:00Z (19:00 Dublin)")
	}
}

func TestTz_WinterOccurrenceFollowsDST_RT18_5(t *testing.T) {
	f := newGateFixture(t)
	// Same 19:00-Dublin weekly series; 2026-12-01 is winter (GMT, UTC+0), so
	// 19:00 Dublin == 19:00Z, not the summer 18:00Z.
	f.registerRecurring(t, "z", mustRFC3339(t, "2026-08-11T18:00:00Z"), weeklyTz(1, "Europe/Dublin"))
	f.now = mustRFC3339(t, "2026-12-01T19:00:00Z")
	if !f.admitted(t, "z") {
		t.Error("winter occurrence should admit at 19:00Z (DST followed)")
	}
	f.now = mustRFC3339(t, "2026-12-01T18:00:00Z")
	if f.admitted(t, "z") {
		t.Error("winter occurrence should NOT admit at the summer 18:00Z instant")
	}
}

// --- AC18.3: DST boundary edge cases resolve to a defined occurrence ---

func TestTz_SpringForwardGap_RT18_6(t *testing.T) {
	f := newGateFixture(t)
	loc := dublinLoc(t)
	// 01:30 local on the spring-forward Sunday does not exist; the stdlib
	// normalizes it to a defined instant rather than erroring.
	anchor := time.Date(2026, 3, 29, 1, 30, 0, 0, loc)
	f.registerRecurring(t, "z", anchor, weeklyTz(1, "Europe/Dublin"))
	f.now = anchor
	if !f.admitted(t, "z") {
		t.Error("spring-forward gap should yield a defined occurrence and admit at it")
	}
}

func TestTz_FallBackOverlap_RT18_7(t *testing.T) {
	f := newGateFixture(t)
	loc := dublinLoc(t)
	// 01:30 local on the fall-back Sunday occurs twice; the stdlib resolves it
	// to a single defined instant.
	anchor := time.Date(2026, 10, 25, 1, 30, 0, 0, loc)
	f.registerRecurring(t, "z", anchor, weeklyTz(1, "Europe/Dublin"))
	f.now = anchor
	if !f.admitted(t, "z") {
		t.Error("fall-back overlap should yield a single defined occurrence and admit at it")
	}
}

// --- AC18.4: CLI accepts and validates the timezone ---

func TestTz_CLIValidZone_RT18_8(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := runMeet(t, dir, "create", "--room", "z",
		"--repeat", "weekly", "--weekday", "tue", "--at", "18:00", "--tz", "Europe/Dublin")
	if code != 0 {
		t.Fatalf("exit=%d stderr=%q", code, stderr)
	}
	if csv := readRoomsCSV(t, dir); !strings.Contains(csv, "Europe/Dublin") {
		t.Errorf("rooms.csv missing timezone; got:\n%s", csv)
	}
}

func TestTz_CLIUnknownZone_RT18_9(t *testing.T) {
	dir := t.TempDir()
	_, _, code := runMeet(t, dir, "create", "--room", "z",
		"--repeat", "weekly", "--weekday", "tue", "--at", "18:00", "--tz", "Nowhere/Nothing")
	if code == 0 {
		t.Error("unknown timezone should exit non-zero")
	}
	if strings.Contains(readRoomsCSV(t, dir), "z,created") {
		t.Error("unknown timezone should write no row")
	}
}
