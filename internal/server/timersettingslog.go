// ABOUTME: Timer settings log (issue #15). Append-only CSV in the state
// ABOUTME: directory; for each room, the latest row is its timer configuration.

package server

import (
	"encoding/csv"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

const timerSettingsFile = "timer_settings.csv"

// DefaultTimerConfig is the configuration used for a room that has never had
// timer settings set (issue #15: 15:00 total, 20% early-warning, 30% grace).
var DefaultTimerConfig = TimerConfig{Total: 900, WarnPercent: 20, GracePercent: 30}

// TimerConfig is a room's timer configuration. Total is in seconds; the
// early-warning and grace values are percentages of the total.
type TimerConfig struct {
	Total        int
	WarnPercent  int
	GracePercent int
}

// WarnSeconds is the size of the final warning stretch (the amber window),
// rounded to the nearest second.
func (c TimerConfig) WarnSeconds() int { return roundPercent(c.Total, c.WarnPercent) }

// GraceSeconds is the grace count-up limit, rounded to the nearest second.
func (c TimerConfig) GraceSeconds() int { return roundPercent(c.Total, c.GracePercent) }

// Valid reports whether the configuration is acceptable: a positive total and
// percentages within 0..100.
func (c TimerConfig) Valid() error {
	if c.Total <= 0 {
		return fmt.Errorf("total must be positive, got %d", c.Total)
	}
	if c.WarnPercent < 0 || c.WarnPercent > 100 {
		return fmt.Errorf("early-warning percent must be 0..100, got %d", c.WarnPercent)
	}
	if c.GracePercent < 0 || c.GracePercent > 100 {
		return fmt.Errorf("grace percent must be 0..100, got %d", c.GracePercent)
	}
	return nil
}

func roundPercent(total, percent int) int {
	return int(math.Round(float64(total) * float64(percent) / 100.0))
}

type timerSettingsEntry struct {
	Timestamp time.Time
	Room      string
	Config    TimerConfig
}

// TimerSettingsLog manages an append-only CSV of per-room timer configuration.
// Same last-row-wins model as RoomsLog; kept separate because timer settings
// change independently of the room registry lifecycle.
type TimerSettingsLog struct {
	mu       sync.Mutex
	filePath string
}

// NewTimerSettingsLog opens or creates the timer settings log under stateDir,
// writing a header row when the file does not exist yet.
func NewTimerSettingsLog(stateDir string) (*TimerSettingsLog, error) {
	filePath := filepath.Join(stateDir, timerSettingsFile)

	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		if err := os.MkdirAll(stateDir, 0o700); err != nil {
			return nil, fmt.Errorf("creating state dir: %w", err)
		}
		f, err := os.Create(filePath)
		if err != nil {
			return nil, fmt.Errorf("creating timer settings log: %w", err)
		}
		w := csv.NewWriter(f)
		if err := w.Write(timerSettingsHeader()); err != nil {
			f.Close()
			return nil, fmt.Errorf("writing header: %w", err)
		}
		w.Flush()
		if err := f.Close(); err != nil {
			return nil, fmt.Errorf("closing timer settings log: %w", err)
		}
	} else if err != nil {
		return nil, fmt.Errorf("statting timer settings log: %w", err)
	}

	return &TimerSettingsLog{filePath: filePath}, nil
}

func timerSettingsHeader() []string {
	return []string{"timestamp", "room", "total_seconds", "warn_percent", "grace_percent"}
}

// Append records a new configuration for a room. The configuration must be
// valid; validation is the caller's responsibility.
func (l *TimerSettingsLog) Append(room string, cfg TimerConfig, now time.Time) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	f, err := os.OpenFile(l.filePath, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("opening timer settings log: %w", err)
	}
	defer f.Close()

	w := csv.NewWriter(f)
	row := []string{
		now.UTC().Format(time.RFC3339),
		room,
		strconv.Itoa(cfg.Total),
		strconv.Itoa(cfg.WarnPercent),
		strconv.Itoa(cfg.GracePercent),
	}
	if err := w.Write(row); err != nil {
		return fmt.Errorf("writing row: %w", err)
	}
	w.Flush()
	return w.Error()
}

// ConfigFor returns the room's latest configuration, or DefaultTimerConfig when
// none has been set.
func (l *TimerSettingsLog) ConfigFor(room string) TimerConfig {
	l.mu.Lock()
	defer l.mu.Unlock()

	rows, err := l.readAll()
	if err != nil {
		return DefaultTimerConfig
	}
	for i := len(rows) - 1; i >= 0; i-- {
		if rows[i].Room == room {
			return rows[i].Config
		}
	}
	return DefaultTimerConfig
}

// readAll parses every data row in the log, in file order. Caller holds mu.
func (l *TimerSettingsLog) readAll() ([]timerSettingsEntry, error) {
	f, err := os.Open(l.filePath)
	if err != nil {
		return nil, fmt.Errorf("opening timer settings log: %w", err)
	}
	defer f.Close()

	r := csv.NewReader(f)
	r.FieldsPerRecord = -1

	var rows []timerSettingsEntry
	first := true
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parsing timer settings log: %w", err)
		}
		if first {
			first = false
			continue
		}
		e, ok := decodeTimerSettings(rec)
		if !ok {
			continue
		}
		rows = append(rows, e)
	}
	return rows, nil
}

func decodeTimerSettings(rec []string) (timerSettingsEntry, bool) {
	if len(rec) < 5 {
		return timerSettingsEntry{}, false
	}
	ts, err := time.Parse(time.RFC3339, rec[0])
	if err != nil {
		return timerSettingsEntry{}, false
	}
	total, err := strconv.Atoi(rec[2])
	if err != nil {
		return timerSettingsEntry{}, false
	}
	warn, err := strconv.Atoi(rec[3])
	if err != nil {
		return timerSettingsEntry{}, false
	}
	grace, err := strconv.Atoi(rec[4])
	if err != nil {
		return timerSettingsEntry{}, false
	}
	return timerSettingsEntry{
		Timestamp: ts,
		Room:      rec[1],
		Config:    TimerConfig{Total: total, WarnPercent: warn, GracePercent: grace},
	}, true
}
