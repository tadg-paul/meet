// ABOUTME: Rooms registry log (issue #7). Append-only CSV in the state
// ABOUTME: directory; for each room, the latest row determines current state.

package server

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"time"
)

const roomsLogFile = "rooms.csv"

// RoomStatus is the lifecycle state of a registered room.
type RoomStatus string

const (
	RoomCreated   RoomStatus = "created"
	RoomCancelled RoomStatus = "cancelled"
)

// RoomLogEntry is one row in rooms.csv. Times are RFC3339 UTC on disk.
type RoomLogEntry struct {
	Timestamp  time.Time
	Room       string
	Status     RoomStatus
	ValidFrom  time.Time
	ValidUntil time.Time
	Note       string
	// Recurrence, when non-nil, makes this a recurring definition (#17): the
	// row's ValidFrom is the anchor and ValidUntil is unused. Nil means a
	// one-off window as in #7.
	Recurrence *Recurrence
}

// RoomsLog manages an append-only CSV of room lifecycle events.
type RoomsLog struct {
	mu       sync.Mutex
	filePath string
}

// NewRoomsLog opens or creates the rooms log under stateDir. Writes a header
// row if the file does not exist yet.
func NewRoomsLog(stateDir string) (*RoomsLog, error) {
	filePath := filepath.Join(stateDir, roomsLogFile)

	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		if err := os.MkdirAll(stateDir, 0o700); err != nil {
			return nil, fmt.Errorf("creating state dir: %w", err)
		}
		f, err := os.Create(filePath)
		if err != nil {
			return nil, fmt.Errorf("creating rooms log: %w", err)
		}
		w := csv.NewWriter(f)
		if err := w.Write(roomsLogHeader()); err != nil {
			f.Close()
			return nil, fmt.Errorf("writing header: %w", err)
		}
		w.Flush()
		if err := f.Close(); err != nil {
			return nil, fmt.Errorf("closing rooms log: %w", err)
		}
	} else if err != nil {
		return nil, fmt.Errorf("statting rooms log: %w", err)
	}

	return &RoomsLog{filePath: filePath}, nil
}

func roomsLogHeader() []string {
	return []string{
		"timestamp", "room", "status", "valid_from", "valid_until", "note",
		"recur_kind", "recur_interval", "recur_ordinal", "recur_weekday",
		"recur_duration_s", "recur_lead_s", "recur_ends", "recur_tz",
	}
}

// Append writes a new entry to the log.
func (l *RoomsLog) Append(entry RoomLogEntry) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	f, err := os.OpenFile(l.filePath, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("opening rooms log: %w", err)
	}
	defer f.Close()

	w := csv.NewWriter(f)
	if err := w.Write(encodeEntry(entry)); err != nil {
		return fmt.Errorf("writing row: %w", err)
	}
	w.Flush()
	return w.Error()
}

// LatestByRoom returns the most recent entry for the given room name, or nil
// if no entry exists.
func (l *RoomsLog) LatestByRoom(room string) *RoomLogEntry {
	l.mu.Lock()
	defer l.mu.Unlock()

	rows, err := l.readAll()
	if err != nil {
		return nil
	}
	for i := len(rows) - 1; i >= 0; i-- {
		if rows[i].Room == room {
			e := rows[i]
			return &e
		}
	}
	return nil
}

// All returns one entry per registered room, each carrying that room's latest
// state. Order is by Timestamp ascending of the latest entry.
func (l *RoomsLog) All() ([]RoomLogEntry, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	rows, err := l.readAll()
	if err != nil {
		return nil, err
	}

	latest := make(map[string]RoomLogEntry)
	for _, r := range rows {
		latest[r.Room] = r // last write wins, since rows are in file order
	}

	out := make([]RoomLogEntry, 0, len(latest))
	for _, e := range latest {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Timestamp.Before(out[j].Timestamp)
	})
	return out, nil
}

// readAll parses every data row in the log, in file order. Caller holds mu.
func (l *RoomsLog) readAll() ([]RoomLogEntry, error) {
	f, err := os.Open(l.filePath)
	if err != nil {
		return nil, fmt.Errorf("opening rooms log: %w", err)
	}
	defer f.Close()

	r := csv.NewReader(f)
	r.FieldsPerRecord = -1

	var rows []RoomLogEntry
	first := true
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parsing rooms log: %w", err)
		}
		if first {
			first = false
			continue
		}
		e, ok := decodeEntry(rec)
		if !ok {
			continue
		}
		rows = append(rows, e)
	}
	return rows, nil
}

func encodeEntry(e RoomLogEntry) []string {
	row := []string{
		e.Timestamp.UTC().Format(time.RFC3339),
		e.Room,
		string(e.Status),
		formatOptional(e.ValidFrom),
		formatOptional(e.ValidUntil),
		e.Note,
	}
	if e.Recurrence != nil {
		r := e.Recurrence
		row = append(row,
			string(r.Kind),
			strconv.Itoa(r.IntervalWeeks),
			strconv.Itoa(r.Ordinal),
			strconv.Itoa(int(r.Weekday)),
			strconv.Itoa(int(r.Duration/time.Second)),
			strconv.Itoa(int(r.Lead/time.Second)),
			formatOptional(r.Ends),
			r.Tz,
		)
	}
	return row
}

func formatOptional(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// CreateRoom appends a "created" entry for the given room. Returns an error
// if the inputs are not internally consistent (empty room, from >= until).
// Does not check whether the room is already registered — the registry is
// last-row-wins, so multiple creates simply overwrite earlier state.
func CreateRoom(log *RoomsLog, room string, from, until time.Time, note string, now time.Time) error {
	if room == "" {
		return fmt.Errorf("room name is required")
	}
	if !from.Before(until) {
		return fmt.Errorf("--from (%s) must be before --until (%s)",
			from.UTC().Format(time.RFC3339), until.UTC().Format(time.RFC3339))
	}
	return log.Append(RoomLogEntry{
		Timestamp:  now,
		Room:       room,
		Status:     RoomCreated,
		ValidFrom:  from,
		ValidUntil: until,
		Note:       note,
	})
}

// CancelRoom appends a "cancelled" entry. Returns an error if the room has
// never been registered (you cannot cancel something that doesn't exist).
func CancelRoom(log *RoomsLog, room string, note string, now time.Time) error {
	if room == "" {
		return fmt.Errorf("room name is required")
	}
	if log.LatestByRoom(room) == nil {
		return fmt.Errorf("room %q is not registered", room)
	}
	return log.Append(RoomLogEntry{
		Timestamp: now,
		Room:      room,
		Status:    RoomCancelled,
		Note:      note,
	})
}

// RoomFilter narrows the output of ListRooms.
type RoomFilter string

const (
	FilterAll       RoomFilter = "all"
	FilterActive    RoomFilter = "active"
	FilterUpcoming  RoomFilter = "upcoming"
	FilterPast      RoomFilter = "past"
	FilterCancelled RoomFilter = "cancelled"
	// FilterCurrent is rooms active now or in the future: one-off rooms not yet
	// past, and recurring rooms not past their end (#19). The default for list.
	FilterCurrent RoomFilter = "current"
)

// ListRooms returns rooms matching the given filter, with one entry per room
// reflecting that room's latest state.
//
//   - active:    latest=created and now ∈ [valid_from, valid_until]
//   - upcoming:  latest=created and now < valid_from
//   - past:      latest=created and now > valid_until
//   - cancelled: latest=cancelled (regardless of window)
//   - all:       every room
func ListRooms(log *RoomsLog, filter RoomFilter, now time.Time) ([]RoomLogEntry, error) {
	all, err := log.All()
	if err != nil {
		return nil, err
	}
	if filter == "" || filter == FilterAll {
		return all, nil
	}
	out := make([]RoomLogEntry, 0, len(all))
	for _, e := range all {
		if matchesFilter(e, filter, now) {
			out = append(out, e)
		}
	}
	return out, nil
}

func matchesFilter(e RoomLogEntry, filter RoomFilter, now time.Time) bool {
	if filter == FilterCancelled {
		return e.Status == RoomCancelled
	}
	if e.Status != RoomCreated {
		return false
	}
	if e.Recurrence != nil {
		ended := !e.Recurrence.Ends.IsZero() && now.After(e.Recurrence.Ends)
		switch filter {
		case FilterActive:
			return e.Recurrence.ActiveAt(e.ValidFrom, now)
		case FilterUpcoming, FilterCurrent:
			return !ended
		case FilterPast:
			return ended
		}
		return false
	}
	switch filter {
	case FilterActive:
		return !now.Before(e.ValidFrom) && !now.After(e.ValidUntil)
	case FilterUpcoming:
		return now.Before(e.ValidFrom)
	case FilterPast:
		return now.After(e.ValidUntil)
	case FilterCurrent:
		return !now.After(e.ValidUntil)
	}
	return false
}

func decodeEntry(rec []string) (RoomLogEntry, bool) {
	if len(rec) < 3 {
		return RoomLogEntry{}, false
	}
	ts, err := time.Parse(time.RFC3339, rec[0])
	if err != nil {
		return RoomLogEntry{}, false
	}
	e := RoomLogEntry{
		Timestamp: ts,
		Room:      rec[1],
		Status:    RoomStatus(rec[2]),
	}
	if len(rec) >= 4 && rec[3] != "" {
		if t, err := time.Parse(time.RFC3339, rec[3]); err == nil {
			e.ValidFrom = t
		}
	}
	if len(rec) >= 5 && rec[4] != "" {
		if t, err := time.Parse(time.RFC3339, rec[4]); err == nil {
			e.ValidUntil = t
		}
	}
	if len(rec) >= 6 {
		e.Note = rec[5]
	}
	if len(rec) >= 13 && rec[6] != "" {
		e.Recurrence = decodeRecurrence(rec)
	}
	return e, true
}

func decodeRecurrence(rec []string) *Recurrence {
	interval, _ := strconv.Atoi(rec[7])
	ordinal, _ := strconv.Atoi(rec[8])
	weekday, _ := strconv.Atoi(rec[9])
	durationSecs, _ := strconv.Atoi(rec[10])
	leadSecs, _ := strconv.Atoi(rec[11])
	var ends time.Time
	if rec[12] != "" {
		if t, err := time.Parse(time.RFC3339, rec[12]); err == nil {
			ends = t
		}
	}
	tz := ""
	if len(rec) >= 14 {
		tz = rec[13]
	}
	return &Recurrence{
		Kind:          RecurKind(rec[6]),
		IntervalWeeks: interval,
		Ordinal:       ordinal,
		Weekday:       time.Weekday(weekday),
		Duration:      time.Duration(durationSecs) * time.Second,
		Lead:          time.Duration(leadSecs) * time.Second,
		Ends:          ends,
		Tz:            tz,
	}
}
