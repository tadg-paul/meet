// ABOUTME: Webhook handler for 8x8 JaaS events. Downloads recordings,
// ABOUTME: transcriptions, and chat logs, then uploads them to Nextcloud via WebDAV.

package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// WebDAVConfig holds Nextcloud WebDAV credentials and destination.
type WebDAVConfig struct {
	URL      string
	Path     string
	User     string
	Password string
}

// webhookPayload is the common structure for all 8x8 webhook events.
type webhookPayload struct {
	EventType      string          `json:"eventType"`
	IdempotencyKey string          `json:"idempotencyKey"`
	AppID          string          `json:"appId"`
	SessionID      string          `json:"sessionId"`
	Timestamp      int64           `json:"timestamp"`
	FQN            string          `json:"fqn"`
	Data           json.RawMessage `json:"data"`
}

// downloadEventData holds fields from events that carry a preAuthenticatedLink.
type downloadEventData struct {
	PreAuthenticatedLink string `json:"preAuthenticatedLink"`
	DurationSec          int    `json:"durationSec"`
	StartTimestamp       int64  `json:"startTimestamp"`
	EndTimestamp         int64  `json:"endTimestamp"`
	RecordingSessionID   string `json:"recordingSessionId"`
}

// deduplicator tracks seen idempotency keys to prevent reprocessing.
type deduplicator struct {
	mu      sync.Mutex
	seen    map[string]time.Time
	maxSize int
}

func newDeduplicator(maxSize int) *deduplicator {
	return &deduplicator{
		seen:    make(map[string]time.Time),
		maxSize: maxSize,
	}
}

// isDuplicate returns true if the key has been seen before. If not, it records the key.
func (d *deduplicator) isDuplicate(key string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()

	if _, exists := d.seen[key]; exists {
		return true
	}

	// Evict oldest entries if at capacity.
	if len(d.seen) >= d.maxSize {
		var oldestKey string
		var oldestTime time.Time
		for k, t := range d.seen {
			if oldestKey == "" || t.Before(oldestTime) {
				oldestKey = k
				oldestTime = t
			}
		}
		delete(d.seen, oldestKey)
	}

	d.seen[key] = time.Now()
	return false
}

// downloadEventTypes are the event types that carry a preAuthenticatedLink.
var downloadEventTypes = map[string]string{
	"RECORDING_UPLOADED":     "recording",
	"TRANSCRIPTION_UPLOADED": "transcript",
	"CHAT_UPLOADED":          "chat",
}

// RecoverPendingUploads scans the download directory on startup for files
// that were downloaded but not yet uploaded (e.g. after a crash or restart).
func (s *Server) RecoverPendingUploads() {
	if s.cfg.WebDAV == nil {
		return
	}

	downloadDir := s.downloadDir()
	entries, err := os.ReadDir(downloadDir)
	if err != nil {
		// Directory doesn't exist yet — nothing to recover.
		return
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		filename := entry.Name()
		localPath := filepath.Join(downloadDir, filename)
		s.logger.Info("startup recovery: found pending upload", "filename", filename)
		go s.uploadWithRetry(localPath, filename)
	}
}

func (s *Server) handleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Authenticate.
	if s.cfg.WebhookToken != "" {
		auth := r.Header.Get("Authorization")
		if auth != s.cfg.WebhookToken {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
	}

	// Parse payload.
	var payload webhookPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	// Log every authenticated event.
	room := extractRoom(payload.FQN)
	s.logger.Info("webhook event",
		"event_type", payload.EventType,
		"room", room,
		"session_id", payload.SessionID,
		"timestamp", payload.Timestamp,
		"idempotency_key", payload.IdempotencyKey,
	)

	// Check if this is a download event.
	fileType, isDownloadEvent := downloadEventTypes[payload.EventType]
	if !isDownloadEvent {
		w.WriteHeader(http.StatusOK)
		return
	}

	// Check storage is configured for this file type. Video recordings go
	// to Cloudflare Stream (issue #6); transcripts and chat continue to use
	// WebDAV. If neither backend is wired for the requested file type, the
	// webhook returns 503 so 8x8 retries until the operator fixes config.
	switch fileType {
	case "recording":
		if s.cfg.Stream == nil {
			http.Error(w, "stream upload not configured", http.StatusServiceUnavailable)
			return
		}
	case "transcript", "chat":
		if s.cfg.WebDAV == nil {
			http.Error(w, "webdav not configured", http.StatusServiceUnavailable)
			return
		}
	}

	// Deduplicate.
	if s.dedup.isDuplicate(payload.IdempotencyKey) {
		s.logger.Info("webhook duplicate, skipping", "idempotency_key", payload.IdempotencyKey)
		w.WriteHeader(http.StatusOK)
		return
	}

	// Parse download-specific data.
	var data downloadEventData
	if err := json.Unmarshal(payload.Data, &data); err != nil {
		s.logger.Error("webhook: failed to parse event data", "error", err)
		http.Error(w, "invalid event data", http.StatusBadRequest)
		return
	}

	// Respond immediately — process asynchronously.
	w.WriteHeader(http.StatusOK)

	go s.processDownload(room, fileType, payload.SessionID, data)
}

func (s *Server) processDownload(room, fileType, sessionID string, data downloadEventData) {
	filename := buildFilename(room, fileType, data)
	logger := s.logger.With("room", room, "file_type", fileType, "filename", filename)

	logger.Info("downloading file", "url_length", len(data.PreAuthenticatedLink))

	// Download to the download directory.
	localPath, err := s.downloadToDir(data.PreAuthenticatedLink, filename)
	if err != nil {
		logger.Error("download failed", "error", err)
		return
	}

	// Route by file type. Recordings go to Cloudflare Stream (#6); transcripts
	// and chat continue to use WebDAV.
	if fileType == "recording" {
		s.uploadRecordingWithRetry(localPath, filename, room, sessionID, data)
		return
	}
	s.uploadWithRetry(localPath, filename)
}

// uploadWithRetry attempts to upload a file to WebDAV with exponential backoff.
// On success, moves the file from download/ to uploaded/. On final failure, the
// file remains in download/ for recovery on next startup.
func (s *Server) uploadWithRetry(localPath, filename string) {
	logger := s.logger.With("filename", filename)

	// Retry schedule: 0, 1m, 2m, 4m, 8m, 16m, 32m, 64m, 128m, 256m, 512m, 1024m (capped at 24h total)
	delay := time.Duration(0)
	maxDelay := 24 * time.Hour
	totalWaited := time.Duration(0)

	for {
		if delay > 0 {
			logger.Info("retrying WebDAV upload", "delay", delay.String(), "total_waited", totalWaited.String())
			time.Sleep(delay)
			totalWaited += delay
		}

		err := s.uploadToWebDAV(localPath, filename)
		if err == nil {
			// Success — move to uploaded directory.
			uploadedPath := filepath.Join(s.uploadedDir(), filename)
			if moveErr := os.Rename(localPath, uploadedPath); moveErr != nil {
				logger.Warn("failed to move to uploaded dir, removing instead", "error", moveErr)
				os.Remove(localPath)
			}
			logger.Info("file uploaded to Nextcloud", "destination", path.Join(s.cfg.WebDAV.Path, filename))
			return
		}

		logger.Error("WebDAV upload failed", "error", err, "total_waited", totalWaited.String())

		// Calculate next delay.
		if delay == 0 {
			delay = 1 * time.Minute
		} else {
			delay = delay * 2
		}

		if totalWaited+delay > maxDelay {
			logger.Error("giving up WebDAV upload after retries — file kept in download dir for manual recovery",
				"path", localPath, "total_waited", totalWaited.String())
			return
		}
	}
}

// uploadRecordingWithRetry uploads a video file to Cloudflare Stream with
// exponential backoff retry, mirroring uploadWithRetry. On success it
// records the upload in recordings.csv, writes a markdown notification to
// the configured Nextcloud share, and moves the file from download/ to
// uploaded/. On final failure the file stays in download/ for recovery.
func (s *Server) uploadRecordingWithRetry(localPath, filename, room, sessionID string, data downloadEventData) {
	logger := s.logger.With("filename", filename, "room", room, "file_type", "recording")

	delay := time.Duration(0)
	maxDelay := 24 * time.Hour
	totalWaited := time.Duration(0)

	for {
		if delay > 0 {
			logger.Info("retrying Stream upload", "delay", delay.String(), "total_waited", totalWaited.String())
			time.Sleep(delay)
			totalWaited += delay
		}

		res, err := s.cfg.Stream.Upload(localPath)
		if err == nil {
			s.afterRecordingUploadSuccess(logger, localPath, filename, room, sessionID, data, res)
			return
		}
		logger.Error("Stream upload failed", "error", err, "total_waited", totalWaited.String())

		if delay == 0 {
			delay = 1 * time.Minute
		} else {
			delay = delay * 2
		}
		if totalWaited+delay > maxDelay {
			logger.Error("giving up Stream upload after retries — file kept in download dir for manual recovery",
				"path", localPath, "total_waited", totalWaited.String())
			return
		}
	}
}

// afterRecordingUploadSuccess does the bookkeeping that turns a successful CF
// Stream upload into the user-visible artefacts: a row in recordings.csv, a
// markdown notification PUT to the Nextcloud share, and a move to uploaded/.
// Any individual post-success step is best-effort and logged on failure; we
// do not redo the upload because Cloudflare already has the video.
func (s *Server) afterRecordingUploadSuccess(
	logger *slog.Logger,
	localPath, filename, room, sessionID string,
	data downloadEventData,
	res *StreamUploadResult,
) {
	playbackURL := s.cfg.PlayerBaseURL + res.UID
	logger.Info("file uploaded to Cloudflare Stream",
		"uid", res.UID, "playback_url", playbackURL,
		"scheduled_deletion", res.ScheduledDeletion.UTC().Format(time.RFC3339),
	)

	// Append a row to recordings.csv.
	if s.cfg.Recordings != nil {
		err := s.cfg.Recordings.Append(RecordingLogEntry{
			Timestamp:         time.Now().UTC(),
			Room:              room,
			SessionID:         sessionID,
			StreamUID:         res.UID,
			PlaybackURL:       playbackURL,
			ScheduledDeletion: res.ScheduledDeletion,
		})
		if err != nil {
			logger.Warn("appending recordings.csv row failed", "error", err)
		}
	}

	// Build and PUT the markdown notification to Nextcloud.
	if s.cfg.WebDAV != nil {
		meta := NotificationMeta{
			Room:              room,
			SessionTimestamp:  sessionTimestamp(data),
			Duration:          time.Duration(data.DurationSec) * time.Second,
			PlaybackURL:       playbackURL,
			ScheduledDeletion: res.ScheduledDeletion,
		}
		body, err := buildNotificationMarkdown(meta)
		if err != nil {
			logger.Warn("rendering notification markdown failed", "error", err)
		} else {
			notifName := notificationFilename(meta)
			if err := s.uploadBytesToWebDAV(body, notifName); err != nil {
				logger.Warn("notification PUT to Nextcloud failed", "error", err, "filename", notifName)
			} else {
				logger.Info("notification document written to Nextcloud", "filename", notifName)
			}
		}
	}

	// Move local file to uploaded/.
	uploadedPath := filepath.Join(s.uploadedDir(), filename)
	if moveErr := os.Rename(localPath, uploadedPath); moveErr != nil {
		logger.Warn("failed to move recording to uploaded dir, removing instead", "error", moveErr)
		os.Remove(localPath)
	}
}

// sessionTimestamp returns the session start time from the webhook data,
// falling back to "now" when 8x8 omits startTimestamp (issue #2 mitigation).
func sessionTimestamp(data downloadEventData) time.Time {
	if data.StartTimestamp > 0 {
		return time.UnixMilli(data.StartTimestamp).UTC()
	}
	return time.Now().UTC()
}

// PurgeOldUploads removes files older than maxAge from the uploaded directory.
func (s *Server) PurgeOldUploads(maxAge time.Duration) {
	uploadedDir := s.uploadedDir()
	entries, err := os.ReadDir(uploadedDir)
	if err != nil {
		return
	}

	cutoff := time.Now().Add(-maxAge)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			path := filepath.Join(uploadedDir, entry.Name())
			if err := os.Remove(path); err != nil {
				s.logger.Warn("failed to purge old upload", "path", path, "error", err)
			} else {
				s.logger.Info("purged old upload", "path", path, "age", time.Since(info.ModTime()).String())
			}
		}
	}
}

func (s *Server) downloadDir() string {
	dir := filepath.Join(s.cfg.DataDir, "download")
	os.MkdirAll(dir, 0o750)
	return dir
}

func (s *Server) uploadedDir() string {
	dir := filepath.Join(s.cfg.DataDir, "uploaded")
	os.MkdirAll(dir, 0o750)
	return dir
}

func buildFilename(room, fileType string, data downloadEventData) string {
	t := time.UnixMilli(data.StartTimestamp).UTC()
	datePart := t.Format("2006-01-02")
	timePart := t.Format("1504")

	switch fileType {
	case "recording":
		return fmt.Sprintf("%s_%s_%s_%ds.mp4", room, datePart, timePart, data.DurationSec)
	case "transcript":
		ext := "json"
		if data.PreAuthenticatedLink != "" {
			ext = extensionFromURL(data.PreAuthenticatedLink, "json")
		}
		return fmt.Sprintf("%s_%s_%s_transcript.%s", room, datePart, timePart, ext)
	case "chat":
		ext := "json"
		if data.PreAuthenticatedLink != "" {
			ext = extensionFromURL(data.PreAuthenticatedLink, "json")
		}
		return fmt.Sprintf("%s_%s_%s_chat.%s", room, datePart, timePart, ext)
	default:
		return fmt.Sprintf("%s_%s_%s_%s", room, datePart, timePart, fileType)
	}
}

// extensionFromURL extracts the file extension from a URL path, or returns the fallback.
func extensionFromURL(rawURL, fallback string) string {
	idx := strings.LastIndex(rawURL, "/")
	if idx < 0 {
		return fallback
	}
	segment := rawURL[idx+1:]
	if q := strings.Index(segment, "?"); q >= 0 {
		segment = segment[:q]
	}
	if dot := strings.LastIndex(segment, "."); dot >= 0 {
		ext := segment[dot+1:]
		if ext != "" {
			return ext
		}
	}
	return fallback
}

func extractRoom(fqn string) string {
	parts := strings.SplitN(fqn, "/", 2)
	if len(parts) == 2 {
		return parts[1]
	}
	return fqn
}

func (s *Server) downloadToDir(url, filename string) (string, error) {
	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Get(url)
	if err != nil {
		return "", fmt.Errorf("GET failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GET returned %d", resp.StatusCode)
	}

	destPath := filepath.Join(s.downloadDir(), filename)
	f, err := os.Create(destPath)
	if err != nil {
		return "", fmt.Errorf("create file: %w", err)
	}

	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		os.Remove(destPath)
		return "", fmt.Errorf("download copy: %w", err)
	}

	f.Close()
	return destPath, nil
}

// uploadBytesToWebDAV PUTs an in-memory byte slice to the configured WebDAV
// destination under the given filename. Used for small artefacts like the
// per-recording markdown notification document (#6).
func (s *Server) uploadBytesToWebDAV(body []byte, filename string) error {
	cfg := s.cfg.WebDAV
	destDir := strings.TrimRight(cfg.URL, "/") + "/" + strings.TrimLeft(cfg.Path, "/")
	destFile := destDir + "/" + filename

	client := &http.Client{Timeout: 30 * time.Second}

	mkcolReq, err := http.NewRequest("MKCOL", destDir, nil)
	if err != nil {
		return fmt.Errorf("MKCOL request: %w", err)
	}
	mkcolReq.SetBasicAuth(cfg.User, cfg.Password)
	if mkcolResp, err := client.Do(mkcolReq); err == nil {
		mkcolResp.Body.Close()
	}

	putReq, err := http.NewRequest("PUT", destFile, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("PUT request: %w", err)
	}
	putReq.SetBasicAuth(cfg.User, cfg.Password)
	putReq.ContentLength = int64(len(body))

	putResp, err := client.Do(putReq)
	if err != nil {
		return fmt.Errorf("PUT failed: %w", err)
	}
	defer putResp.Body.Close()

	if putResp.StatusCode < 200 || putResp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(putResp.Body)
		return fmt.Errorf("PUT returned %d: %s", putResp.StatusCode, string(respBody))
	}
	return nil
}

func (s *Server) uploadToWebDAV(localPath, filename string) error {
	cfg := s.cfg.WebDAV
	destDir := strings.TrimRight(cfg.URL, "/") + "/" + strings.TrimLeft(cfg.Path, "/")
	destFile := destDir + "/" + filename

	client := &http.Client{Timeout: 10 * time.Minute}

	// Ensure directory exists (MKCOL). Ignore errors — the directory may already exist.
	mkcolReq, err := http.NewRequest("MKCOL", destDir, nil)
	if err != nil {
		return fmt.Errorf("MKCOL request: %w", err)
	}
	mkcolReq.SetBasicAuth(cfg.User, cfg.Password)
	mkcolResp, err := client.Do(mkcolReq)
	if err != nil {
		s.logger.Warn("WebDAV MKCOL failed (directory may already exist)", "error", err)
	} else {
		mkcolResp.Body.Close()
	}

	// Upload the file.
	f, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("open local file: %w", err)
	}
	defer f.Close()

	putReq, err := http.NewRequest("PUT", destFile, f)
	if err != nil {
		return fmt.Errorf("PUT request: %w", err)
	}
	putReq.SetBasicAuth(cfg.User, cfg.Password)

	putResp, err := client.Do(putReq)
	if err != nil {
		return fmt.Errorf("PUT failed: %w", err)
	}
	defer putResp.Body.Close()

	if putResp.StatusCode < 200 || putResp.StatusCode >= 300 {
		body, _ := io.ReadAll(putResp.Body)
		return fmt.Errorf("PUT returned %d: %s", putResp.StatusCode, string(body))
	}

	return nil
}
