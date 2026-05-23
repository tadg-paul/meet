// ABOUTME: Regression tests for the rooms registry log (issue #7).
// ABOUTME: Append-only CSV in the state directory, last-row-wins per room.

package regression

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tadg-paul/meet/internal/server"
)

func mustRFC3339(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return v
}

func newRoomsLog(t *testing.T) (*server.RoomsLog, string) {
	t.Helper()
	dir := t.TempDir()
	log, err := server.NewRoomsLog(dir)
	if err != nil {
		t.Fatalf("NewRoomsLog: %v", err)
	}
	return log, dir
}

// AC7.5/AC7.7 — empty log: no entries, no errors.
func TestRoomsLog_EmptyStartsClean(t *testing.T) {
	log, dir := newRoomsLog(t)

	got := log.LatestByRoom("does-not-exist")
	if got != nil {
		t.Errorf("LatestByRoom on empty log = %+v, want nil", got)
	}

	all, err := log.All()
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(all) != 0 {
		t.Errorf("All on empty log returned %d entries, want 0", len(all))
	}

	// The CSV file should exist with just a header row.
	csvPath := filepath.Join(dir, "rooms.csv")
	if _, err := os.Stat(csvPath); err != nil {
		t.Fatalf("rooms.csv not created: %v", err)
	}
}

// AC7.3 — created row written and retrievable.
func TestRoomsLog_AppendCreated_RoundTrip(t *testing.T) {
	log, _ := newRoomsLog(t)

	entry := server.RoomLogEntry{
		Timestamp:  mustRFC3339(t, "2026-05-21T10:00:00Z"),
		Room:       "readers-2026-05-22",
		Status:     server.RoomCreated,
		ValidFrom:  mustRFC3339(t, "2026-05-22T19:00:00Z"),
		ValidUntil: mustRFC3339(t, "2026-05-22T21:00:00Z"),
		Note:       "book club",
	}
	if err := log.Append(entry); err != nil {
		t.Fatalf("Append: %v", err)
	}

	got := log.LatestByRoom("readers-2026-05-22")
	if got == nil {
		t.Fatal("LatestByRoom returned nil after Append")
	}
	if got.Status != server.RoomCreated {
		t.Errorf("status = %q, want %q", got.Status, server.RoomCreated)
	}
	if !got.ValidFrom.Equal(entry.ValidFrom) {
		t.Errorf("ValidFrom = %v, want %v", got.ValidFrom, entry.ValidFrom)
	}
	if !got.ValidUntil.Equal(entry.ValidUntil) {
		t.Errorf("ValidUntil = %v, want %v", got.ValidUntil, entry.ValidUntil)
	}
	if got.Note != "book club" {
		t.Errorf("Note = %q, want %q", got.Note, "book club")
	}
}

// AC7.4 + AC7.7 — last-row-wins: created then cancelled = cancelled.
func TestRoomsLog_LastRowWins(t *testing.T) {
	log, _ := newRoomsLog(t)

	created := server.RoomLogEntry{
		Timestamp:  mustRFC3339(t, "2026-05-21T10:00:00Z"),
		Room:       "demo",
		Status:     server.RoomCreated,
		ValidFrom:  mustRFC3339(t, "2026-05-22T19:00:00Z"),
		ValidUntil: mustRFC3339(t, "2026-05-22T21:00:00Z"),
	}
	if err := log.Append(created); err != nil {
		t.Fatalf("Append created: %v", err)
	}

	cancelled := server.RoomLogEntry{
		Timestamp: mustRFC3339(t, "2026-05-22T15:00:00Z"),
		Room:      "demo",
		Status:    server.RoomCancelled,
		Note:      "postponed",
	}
	if err := log.Append(cancelled); err != nil {
		t.Fatalf("Append cancelled: %v", err)
	}

	got := log.LatestByRoom("demo")
	if got == nil {
		t.Fatal("LatestByRoom returned nil")
	}
	if got.Status != server.RoomCancelled {
		t.Errorf("status = %q, want %q (last row wins)", got.Status, server.RoomCancelled)
	}
	if got.Note != "postponed" {
		t.Errorf("Note = %q, want %q", got.Note, "postponed")
	}
}

// AC7.5 — All() returns one entry per room, latest state.
func TestRoomsLog_All_OneEntryPerRoom(t *testing.T) {
	log, _ := newRoomsLog(t)

	mustAppend := func(room string, status server.RoomStatus, when string) {
		err := log.Append(server.RoomLogEntry{
			Timestamp: mustRFC3339(t, when),
			Room:      room,
			Status:    status,
			ValidFrom: mustRFC3339(t, "2026-05-22T19:00:00Z"),
			ValidUntil: mustRFC3339(t, "2026-05-22T21:00:00Z"),
		})
		if err != nil {
			t.Fatalf("Append %s/%s: %v", room, status, err)
		}
	}

	mustAppend("room-a", server.RoomCreated, "2026-05-21T10:00:00Z")
	mustAppend("room-b", server.RoomCreated, "2026-05-21T11:00:00Z")
	mustAppend("room-a", server.RoomCancelled, "2026-05-21T12:00:00Z")
	mustAppend("room-c", server.RoomCreated, "2026-05-21T13:00:00Z")

	all, err := log.All()
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("All returned %d entries, want 3 unique rooms", len(all))
	}

	byRoom := make(map[string]server.RoomLogEntry)
	for _, e := range all {
		byRoom[e.Room] = e
	}
	if byRoom["room-a"].Status != server.RoomCancelled {
		t.Errorf("room-a status = %q, want %q", byRoom["room-a"].Status, server.RoomCancelled)
	}
	if byRoom["room-b"].Status != server.RoomCreated {
		t.Errorf("room-b status = %q, want %q", byRoom["room-b"].Status, server.RoomCreated)
	}
	if byRoom["room-c"].Status != server.RoomCreated {
		t.Errorf("room-c status = %q, want %q", byRoom["room-c"].Status, server.RoomCreated)
	}
}

// AC7.7 — persistence across restart: a fresh RoomsLog over the same state dir
// reads back the same data.
func TestRoomsLog_PersistsAcrossRestart(t *testing.T) {
	dir := t.TempDir()

	first, err := server.NewRoomsLog(dir)
	if err != nil {
		t.Fatalf("NewRoomsLog (1): %v", err)
	}
	created := server.RoomLogEntry{
		Timestamp:  mustRFC3339(t, "2026-05-21T10:00:00Z"),
		Room:       "persist-me",
		Status:     server.RoomCreated,
		ValidFrom:  mustRFC3339(t, "2026-05-22T19:00:00Z"),
		ValidUntil: mustRFC3339(t, "2026-05-22T21:00:00Z"),
		Note:       "should survive restart",
	}
	if err := first.Append(created); err != nil {
		t.Fatalf("Append: %v", err)
	}

	// Simulate restart by constructing a fresh log over the same directory.
	second, err := server.NewRoomsLog(dir)
	if err != nil {
		t.Fatalf("NewRoomsLog (2): %v", err)
	}
	got := second.LatestByRoom("persist-me")
	if got == nil {
		t.Fatal("LatestByRoom on fresh log returned nil; persistence broken")
	}
	if got.Note != "should survive restart" {
		t.Errorf("Note = %q, want %q", got.Note, "should survive restart")
	}
	if !got.ValidFrom.Equal(created.ValidFrom) {
		t.Errorf("ValidFrom = %v, want %v", got.ValidFrom, created.ValidFrom)
	}
}

// LatestByRoom for an unknown room returns nil (not an error).
func TestRoomsLog_LatestByRoom_UnknownRoom(t *testing.T) {
	log, _ := newRoomsLog(t)

	if got := log.LatestByRoom(""); got != nil {
		t.Errorf("LatestByRoom(\"\") = %+v, want nil", got)
	}
	if got := log.LatestByRoom("never-registered"); got != nil {
		t.Errorf("LatestByRoom(\"never-registered\") = %+v, want nil", got)
	}
}

// Header creation: a freshly-created CSV has the canonical header row.
func TestRoomsLog_HeaderIsCanonical(t *testing.T) {
	_, dir := newRoomsLog(t)

	data, err := os.ReadFile(filepath.Join(dir, "rooms.csv"))
	if err != nil {
		t.Fatalf("read rooms.csv: %v", err)
	}
	got := string(data)
	want := "timestamp,room,status,valid_from,valid_until,note\n"
	if got != want {
		t.Errorf("header = %q, want %q", got, want)
	}
}
