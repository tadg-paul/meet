// ABOUTME: Recordings registry (issue #6). Append-only CSV at
// ABOUTME: $STATE_DIRECTORY/recordings.csv tracking video recordings that
// ABOUTME: have been successfully uploaded to Cloudflare Stream.

package server

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const recordingsLogFile = "recordings.csv"

// RecordingLogEntry is one row in recordings.csv.
type RecordingLogEntry struct {
	Timestamp         time.Time
	Room              string
	SessionID         string
	StreamUID         string
	PlaybackURL       string
	ScheduledDeletion time.Time
}

// RecordingsLog manages an append-only CSV of successful CF Stream uploads.
type RecordingsLog struct {
	mu       sync.Mutex
	filePath string
}

// NewRecordingsLog opens or creates the recordings log in stateDir. Creates
// the file with a canonical header if it does not already exist.
func NewRecordingsLog(stateDir string) (*RecordingsLog, error) {
	filePath := filepath.Join(stateDir, recordingsLogFile)
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		if err := os.MkdirAll(stateDir, 0o700); err != nil {
			return nil, fmt.Errorf("creating state dir: %w", err)
		}
		f, err := os.Create(filePath)
		if err != nil {
			return nil, fmt.Errorf("creating recordings log: %w", err)
		}
		w := csv.NewWriter(f)
		if err := w.Write(recordingsLogHeader()); err != nil {
			f.Close()
			return nil, fmt.Errorf("writing header: %w", err)
		}
		w.Flush()
		if err := f.Close(); err != nil {
			return nil, fmt.Errorf("closing recordings log: %w", err)
		}
	} else if err != nil {
		return nil, fmt.Errorf("statting recordings log: %w", err)
	}
	return &RecordingsLog{filePath: filePath}, nil
}

func recordingsLogHeader() []string {
	return []string{"timestamp", "room", "session_id", "stream_uid", "playback_url", "scheduled_deletion"}
}

// Append writes a new row to the recordings log.
func (l *RecordingsLog) Append(entry RecordingLogEntry) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	f, err := os.OpenFile(l.filePath, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("opening recordings log: %w", err)
	}
	defer f.Close()

	w := csv.NewWriter(f)
	if err := w.Write(encodeRecordingEntry(entry)); err != nil {
		return fmt.Errorf("writing row: %w", err)
	}
	w.Flush()
	return w.Error()
}

// All returns every row in the log in file order.
func (l *RecordingsLog) All() ([]RecordingLogEntry, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	f, err := os.Open(l.filePath)
	if err != nil {
		return nil, fmt.Errorf("opening recordings log: %w", err)
	}
	defer f.Close()

	r := csv.NewReader(f)
	r.FieldsPerRecord = -1

	var rows []RecordingLogEntry
	first := true
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parsing recordings log: %w", err)
		}
		if first {
			first = false
			continue
		}
		e, ok := decodeRecordingEntry(rec)
		if !ok {
			continue
		}
		rows = append(rows, e)
	}
	return rows, nil
}

func encodeRecordingEntry(e RecordingLogEntry) []string {
	return []string{
		e.Timestamp.UTC().Format(time.RFC3339),
		e.Room,
		e.SessionID,
		e.StreamUID,
		e.PlaybackURL,
		e.ScheduledDeletion.UTC().Format(time.RFC3339),
	}
}

func decodeRecordingEntry(rec []string) (RecordingLogEntry, bool) {
	if len(rec) < 6 {
		return RecordingLogEntry{}, false
	}
	ts, err := time.Parse(time.RFC3339, rec[0])
	if err != nil {
		return RecordingLogEntry{}, false
	}
	sd, err := time.Parse(time.RFC3339, rec[5])
	if err != nil {
		return RecordingLogEntry{}, false
	}
	return RecordingLogEntry{
		Timestamp:         ts,
		Room:              rec[1],
		SessionID:         rec[2],
		StreamUID:         rec[3],
		PlaybackURL:       rec[4],
		ScheduledDeletion: sd,
	}, true
}
