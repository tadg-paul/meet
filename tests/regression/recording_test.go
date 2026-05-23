// ABOUTME: Regression tests for the Cloudflare Stream recording pipeline
// ABOUTME: (issue #6). Exercises the webhook handler end-to-end against fake
// ABOUTME: 8x8, CF Stream, and WebDAV backends.

package regression

import (
	"encoding/base64"
	"encoding/csv"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tadg-paul/meet/internal/server"
)

// streamFixture wires the webhook server with both backends and exposes the
// fakes for assertions. recordingsLog and stateDir are returned so tests can
// inspect the on-disk artefacts the pipeline produces.
type streamFixture struct {
	ts          *httptest.Server
	stream      *streamHarness
	dav         *httptest.Server
	davPath     *string
	davBody     *[]byte
	davCount    *atomic.Int32
	stateDir    string
	recordings  *server.RecordingsLog
}

func newStreamFixture(t *testing.T) *streamFixture {
	t.Helper()
	var davPath string
	var davBody []byte
	var davCount atomic.Int32
	dav := fakeWebDAVServer(&davPath, &davBody, &davCount)
	t.Cleanup(dav.Close)
	ts, stream := newWebhookTestServerWithStream(t, dav.URL, "/Recordings/meet")
	t.Cleanup(ts.Close)
	return &streamFixture{
		ts:       ts,
		stream:   stream,
		dav:      dav,
		davPath:  &davPath,
		davBody:  &davBody,
		davCount: &davCount,
	}
}

// AC6.1 — RECORDING_UPLOADED routes to Cloudflare Stream, not to WebDAV for
// the mp4 itself.
func TestRecording_RoutesToStream_RT6_1(t *testing.T) {
	dlSrv := fakeDownloadServer("recording-bytes")
	defer dlSrv.Close()

	f := newStreamFixture(t)
	body := webhookPayload("RECORDING_UPLOADED", "key-rt61", "vpaas-magic-cookie-test/foo",
		dlSrv.URL+"/file.mp4", 1776528000000, 60)

	resp := postWebhook(t, f.ts.URL, body)
	resp.Body.Close()

	// CF Stream got a POST + at least one PATCH.
	waitForAtomic(t, &f.stream.createCount, 1, 5)
	if f.stream.patchCount.Load() < 1 {
		t.Errorf("expected at least one PATCH to fake Stream, got %d", f.stream.patchCount.Load())
	}

	// WebDAV received only the notification doc, not the mp4.
	waitForAtomic(t, f.davCount, 1, 5)
	if !strings.HasSuffix(*f.davPath, "_recording.md") {
		t.Errorf("WebDAV upload was not the notification md; path=%q", *f.davPath)
	}
}

// AC6.1 + AC6.3 — TUS handshake completes and recordings.csv has a row with
// the UID returned by Cloudflare.
func TestRecording_TUSCompletes_AndCSVRowAppears_RT6_2_3(t *testing.T) {
	dlSrv := fakeDownloadServer("rec-bytes")
	defer dlSrv.Close()

	// Set up a fixture with a fresh state dir we can read back.
	stateDir := t.TempDir()
	recLog, err := server.NewRecordingsLog(stateDir)
	if err != nil {
		t.Fatalf("NewRecordingsLog: %v", err)
	}
	stream := fakeStreamServer(t)
	streamClient := server.NewStreamClient(server.StreamConfig{
		AccountID: "test-account", APIToken: "test-token",
		TTLDays: 90, APIBase: stream.server.URL,
	}, nil)
	var davPath string
	var davBody []byte
	var davCount atomic.Int32
	dav := fakeWebDAVServer(&davPath, &davBody, &davCount)
	t.Cleanup(dav.Close)

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	srv := server.New(server.Config{
		Addr: "127.0.0.1:0", BaseURL: "https://meet.lobb.ie",
		AppID:    "vpaas-magic-cookie-test",
		DataDir:  stateDir,
		WebDAV: &server.WebDAVConfig{
			URL: dav.URL, Path: "/Recordings/meet",
			User: "u", Password: "p",
		},
		Stream:        streamClient,
		Recordings:    recLog,
		PlayerBaseURL: "https://media.lobb.ie/",
		WebhookToken:  testWebhookToken,
		Logger:        logger,
	})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	body := webhookPayload("RECORDING_UPLOADED", "key-rt62", "vpaas-magic-cookie-test/room",
		dlSrv.URL+"/file.mp4", 1776528000000, 60)
	resp := postWebhook(t, ts.URL, body)
	resp.Body.Close()

	waitForAtomic(t, &stream.createCount, 1, 5)
	waitForAtomic(t, &davCount, 1, 5)

	// Read recordings.csv and confirm a row exists with the UID.
	csvPath := filepath.Join(stateDir, "recordings.csv")
	data, err := os.ReadFile(csvPath)
	if err != nil {
		t.Fatalf("read recordings.csv: %v", err)
	}
	r := csv.NewReader(strings.NewReader(string(data)))
	rows, _ := r.ReadAll()
	if len(rows) < 2 {
		t.Fatalf("recordings.csv has %d rows, want at least header + 1 data row; content=%q", len(rows), data)
	}
	dataRow := rows[1]
	if dataRow[3] != "test-stream-uid" {
		t.Errorf("stream_uid column = %q, want %q", dataRow[3], "test-stream-uid")
	}
	if !strings.HasPrefix(dataRow[4], "https://media.lobb.ie/") {
		t.Errorf("playback_url column = %q, want media.lobb.ie prefix", dataRow[4])
	}
}

// AC6.3 — Upload-Metadata header contains a base64-encoded scheduleddeletion
// approximately upload-time + ttl.
func TestRecording_SchedulesDeletion_RT6_10(t *testing.T) {
	dlSrv := fakeDownloadServer("data")
	defer dlSrv.Close()

	f := newStreamFixture(t)
	body := webhookPayload("RECORDING_UPLOADED", "key-rt6-10", "vpaas-magic-cookie-test/room",
		dlSrv.URL+"/x.mp4", 1776528000000, 60)
	before := time.Now()
	resp := postWebhook(t, f.ts.URL, body)
	resp.Body.Close()
	waitForAtomic(t, &f.stream.createCount, 1, 5)

	// Decode Upload-Metadata, pick out scheduleddeletion, verify it's
	// roughly now + 90 days.
	wanted := decodeTUSMetadata(t, f.stream.lastMetadata)
	sdVal, ok := wanted["scheduleddeletion"]
	if !ok {
		t.Fatalf("Upload-Metadata missing scheduleddeletion; got: %v", wanted)
	}
	sd, err := time.Parse(time.RFC3339, sdVal)
	if err != nil {
		t.Fatalf("scheduleddeletion %q not RFC3339: %v", sdVal, err)
	}
	expectedAt := before.Add(90 * 24 * time.Hour)
	if sd.Before(expectedAt.Add(-1*time.Minute)) || sd.After(expectedAt.Add(1*time.Minute)) {
		t.Errorf("scheduleddeletion %v, want within 1m of %v", sd, expectedAt)
	}
}

// AC6.4 — on success the file is moved from download/ to uploaded/.
func TestRecording_MovesFileToUploaded_RT6_13(t *testing.T) {
	dlSrv := fakeDownloadServer("rec")
	defer dlSrv.Close()

	stateDir := t.TempDir()
	recLog, _ := server.NewRecordingsLog(stateDir)
	stream := fakeStreamServer(t)
	streamClient := server.NewStreamClient(server.StreamConfig{
		AccountID: "x", APIToken: "y", TTLDays: 90, APIBase: stream.server.URL,
	}, nil)
	var davPath string
	var davBody []byte
	var davCount atomic.Int32
	dav := fakeWebDAVServer(&davPath, &davBody, &davCount)
	t.Cleanup(dav.Close)

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	srv := server.New(server.Config{
		Addr: "127.0.0.1:0", BaseURL: "https://meet.lobb.ie",
		AppID:   "vpaas-magic-cookie-test",
		DataDir: stateDir,
		WebDAV: &server.WebDAVConfig{
			URL: dav.URL, Path: "/Recordings/meet",
			User: "u", Password: "p",
		},
		Stream:        streamClient,
		Recordings:    recLog,
		PlayerBaseURL: "https://media.lobb.ie/",
		WebhookToken:  testWebhookToken,
		Logger:        logger,
	})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	body := webhookPayload("RECORDING_UPLOADED", "key-rt6-13", "vpaas-magic-cookie-test/room",
		dlSrv.URL+"/x.mp4", 1776528000000, 60)
	resp := postWebhook(t, ts.URL, body)
	resp.Body.Close()

	waitForAtomic(t, &stream.createCount, 1, 5)
	waitForAtomic(t, &davCount, 1, 5)

	// Allow the os.Rename to settle.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(stateDir, "uploaded", "room_2026-04-18_1600_60s.mp4")); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if _, err := os.Stat(filepath.Join(stateDir, "uploaded", "room_2026-04-18_1600_60s.mp4")); err != nil {
		t.Errorf("recording not in uploaded/: %v", err)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "download", "room_2026-04-18_1600_60s.mp4")); !os.IsNotExist(err) {
		t.Errorf("recording still in download/ (err=%v)", err)
	}
}

// AC6.6 — TRANSCRIPTION_UPLOADED routes to WebDAV, NOT to Cloudflare Stream.
func TestRecording_TranscriptStaysOnWebDAV_RT6_20(t *testing.T) {
	dlSrv := fakeDownloadServer("transcript")
	defer dlSrv.Close()

	f := newStreamFixture(t)
	body := webhookPayload("TRANSCRIPTION_UPLOADED", "key-rt6-20", "vpaas-magic-cookie-test/room",
		dlSrv.URL+"/x.json", 1776528000000, 60)
	resp := postWebhook(t, f.ts.URL, body)
	resp.Body.Close()

	waitForAtomic(t, f.davCount, 1, 5)
	if f.stream.createCount.Load() != 0 {
		t.Errorf("Stream received %d POSTs for transcript, want 0", f.stream.createCount.Load())
	}
}

// AC6.6 — CHAT_UPLOADED routes to WebDAV, NOT to Cloudflare Stream.
func TestRecording_ChatStaysOnWebDAV_RT6_21(t *testing.T) {
	dlSrv := fakeDownloadServer("chat-log")
	defer dlSrv.Close()

	f := newStreamFixture(t)
	body := webhookPayload("CHAT_UPLOADED", "key-rt6-21", "vpaas-magic-cookie-test/room",
		dlSrv.URL+"/x.json", 1776528000000, 60)
	resp := postWebhook(t, f.ts.URL, body)
	resp.Body.Close()

	waitForAtomic(t, f.davCount, 1, 5)
	if f.stream.createCount.Load() != 0 {
		t.Errorf("Stream received %d POSTs for chat, want 0", f.stream.createCount.Load())
	}
}

// AC6.7 — when CF account-id is missing the recording pipeline disables itself;
// a RECORDING_UPLOADED webhook returns 503 (caller-side retryable) and does
// not invoke Stream or WebDAV for the mp4.
func TestRecording_MissingCFConfig_DisablesPipeline_RT6_26(t *testing.T) {
	dlSrv := fakeDownloadServer("data")
	defer dlSrv.Close()
	var davPath string
	var davBody []byte
	var davCount atomic.Int32
	dav := fakeWebDAVServer(&davPath, &davBody, &davCount)
	t.Cleanup(dav.Close)

	// Construct a server without Stream wired.
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	recLog, _ := server.NewRecordingsLog(t.TempDir())
	srv := server.New(server.Config{
		Addr: "127.0.0.1:0", BaseURL: "https://meet.lobb.ie",
		AppID: "vpaas-magic-cookie-test",
		WebDAV: &server.WebDAVConfig{
			URL: dav.URL, Path: "/Recordings/meet",
			User: "u", Password: "p",
		},
		Recordings:   recLog,
		WebhookToken: testWebhookToken,
		Logger:       logger,
	})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	body := webhookPayload("RECORDING_UPLOADED", "key-rt6-26", "vpaas-magic-cookie-test/room",
		dlSrv.URL+"/x.mp4", 1776528000000, 60)
	resp := postWebhook(t, ts.URL, body)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503 (Stream not configured)", resp.StatusCode)
	}
	// Nothing should reach WebDAV either; the handler bails before the
	// download goroutine spawns.
	time.Sleep(100 * time.Millisecond)
	if davCount.Load() != 0 {
		t.Errorf("WebDAV got %d PUTs while Stream was unconfigured, want 0", davCount.Load())
	}
}

func decodeTUSMetadata(t *testing.T, header string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		fields := strings.SplitN(part, " ", 2)
		if len(fields) != 2 {
			continue
		}
		raw, err := base64.StdEncoding.DecodeString(fields[1])
		if err != nil {
			t.Fatalf("decode metadata value %q: %v", fields[1], err)
		}
		out[fields[0]] = string(raw)
	}
	return out
}

