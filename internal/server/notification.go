// ABOUTME: Notification document built for each successful CF Stream upload
// ABOUTME: and PUT to the existing Nextcloud WebDAV share (issue #6).
// ABOUTME: Surfaces "the recording is up" without operator action: the file
// ABOUTME: appearing in Nextcloud is the notification.

package server

import (
	"bytes"
	"fmt"
	"text/template"
	"time"
)

// NotificationMeta is the data the markdown notification template renders.
type NotificationMeta struct {
	Room              string
	SessionTimestamp  time.Time
	Duration          time.Duration
	PlaybackURL       string
	ScheduledDeletion time.Time
}

// notificationFilename returns the file name used inside the configured
// Nextcloud share for the markdown notification associated with a recording.
// Mirrors the {room}_{date}_{time}_<kind>.<ext> convention used for chat and
// transcript filenames.
func notificationFilename(meta NotificationMeta) string {
	t := meta.SessionTimestamp.UTC()
	return fmt.Sprintf("%s_%s_%s_recording.md",
		meta.Room,
		t.Format("2006-01-02"),
		t.Format("1504"),
	)
}

var notificationTmpl = template.Must(template.New("notification").Parse(
	`# {{.Room}} — {{.Date}}

- Session timestamp: {{.SessionTimestamp}}
- Duration: {{.Duration}}
- Playback: <{{.PlaybackURL}}>
- Scheduled deletion: {{.ScheduledDeletion}}
`))

// buildNotificationMarkdown renders the per-recording markdown notification
// the operator finds in the Nextcloud share. Layout is deliberately small and
// scannable.
func buildNotificationMarkdown(meta NotificationMeta) ([]byte, error) {
	data := struct {
		Room              string
		Date              string
		SessionTimestamp  string
		Duration          string
		PlaybackURL       string
		ScheduledDeletion string
	}{
		Room:              meta.Room,
		Date:              meta.SessionTimestamp.UTC().Format("2006-01-02"),
		SessionTimestamp:  meta.SessionTimestamp.UTC().Format(time.RFC3339),
		Duration:          meta.Duration.String(),
		PlaybackURL:       meta.PlaybackURL,
		ScheduledDeletion: meta.ScheduledDeletion.UTC().Format(time.RFC3339),
	}
	var buf bytes.Buffer
	if err := notificationTmpl.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("rendering notification: %w", err)
	}
	return buf.Bytes(), nil
}
