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
	return []string{"timestamp", "room", "status", "valid_from", "valid_until", "note"}
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
	return []string{
		e.Timestamp.UTC().Format(time.RFC3339),
		e.Room,
		string(e.Status),
		formatOptional(e.ValidFrom),
		formatOptional(e.ValidUntil),
		e.Note,
	}
}

func formatOptional(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
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
	return e, true
}
