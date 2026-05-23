// ABOUTME: Cloudflare Stream TUS upload client (issue #6). Uploads a local
// ABOUTME: video file via the TUS resumable protocol and sets the
// ABOUTME: scheduledDeletion field via the Upload-Metadata header so CF
// ABOUTME: deletes the recording after a configurable retention window.

package server

import (
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// StreamConfig holds Cloudflare Stream credentials and tuning.
type StreamConfig struct {
	AccountID string
	APIToken  string
	// TTLDays is how many days after upload Cloudflare should auto-delete
	// the video. Set via the scheduledDeletion field in Upload-Metadata.
	TTLDays int
	// APIBase overrides the api.cloudflare.com base for tests. Empty in
	// production. Must include scheme. No trailing slash.
	APIBase string
}

// StreamUploadResult is what a successful Stream upload returns to the caller.
type StreamUploadResult struct {
	UID               string
	ScheduledDeletion time.Time
}

// StreamClient performs CF Stream TUS uploads.
type StreamClient struct {
	cfg        StreamConfig
	httpClient *http.Client
	now        func() time.Time
	// chunkSize is the size of each TUS PATCH body. Default 50 MiB.
	chunkSize int64
}

// NewStreamClient constructs a CF Stream client. now defaults to time.Now when
// nil so tests can inject a fixed clock for scheduledDeletion assertions.
func NewStreamClient(cfg StreamConfig, now func() time.Time) *StreamClient {
	if now == nil {
		now = time.Now
	}
	return &StreamClient{
		cfg: cfg,
		httpClient: &http.Client{
			// No client-level timeout: large chunk uploads can take minutes.
			// Per CODE/GO.md, surrounding retry loop owns the outer deadline
			// and Transport.ResponseHeaderTimeout catches a hung server.
			Transport: &http.Transport{
				ResponseHeaderTimeout: 60 * time.Second,
				IdleConnTimeout:       90 * time.Second,
			},
		},
		now:       now,
		chunkSize: 50 * 1024 * 1024,
	}
}

func (c *StreamClient) apiBase() string {
	if c.cfg.APIBase != "" {
		return c.cfg.APIBase
	}
	return "https://api.cloudflare.com/client/v4"
}

// Upload sends a local file to Cloudflare Stream via TUS and sets a
// scheduledDeletion that CF will use to auto-delete the video. Returns the
// resulting Stream UID and the scheduled-deletion time we asked CF to honour.
func (c *StreamClient) Upload(localPath string) (*StreamUploadResult, error) {
	if c.cfg.AccountID == "" || c.cfg.APIToken == "" {
		return nil, fmt.Errorf("stream: missing account-id or api-token")
	}

	f, err := os.Open(localPath)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", localPath, err)
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", localPath, err)
	}
	size := fi.Size()

	ttlDays := c.cfg.TTLDays
	if ttlDays <= 0 {
		ttlDays = 90
	}
	scheduledDeletion := c.now().Add(time.Duration(ttlDays) * 24 * time.Hour)

	// POST to create the TUS upload.
	createURL := fmt.Sprintf("%s/accounts/%s/stream", c.apiBase(), c.cfg.AccountID)
	createReq, err := http.NewRequest(http.MethodPost, createURL, nil)
	if err != nil {
		return nil, fmt.Errorf("building create request: %w", err)
	}
	createReq.Header.Set("Authorization", "Bearer "+c.cfg.APIToken)
	createReq.Header.Set("Tus-Resumable", "1.0.0")
	createReq.Header.Set("Upload-Length", strconv.FormatInt(size, 10))
	createReq.Header.Set("Upload-Metadata", encodeUploadMetadata(map[string]string{
		"name":              fi.Name(),
		"scheduleddeletion": scheduledDeletion.UTC().Format(time.RFC3339),
	}))

	createResp, err := c.httpClient.Do(createReq)
	if err != nil {
		return nil, fmt.Errorf("stream POST create %q: %w", localPath, err)
	}
	defer createResp.Body.Close()

	if createResp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(createResp.Body)
		return nil, fmt.Errorf("stream POST create returned %d: %s", createResp.StatusCode, body)
	}

	location := createResp.Header.Get("Location")
	if location == "" {
		return nil, fmt.Errorf("stream POST create: missing Location header")
	}
	uid := createResp.Header.Get("stream-media-id")
	if uid == "" {
		return nil, fmt.Errorf("stream POST create: missing stream-media-id header")
	}

	// PATCH chunks until the file is fully uploaded.
	if err := c.uploadChunks(location, f, size); err != nil {
		return nil, fmt.Errorf("stream PATCH chunks for %q: %w", localPath, err)
	}

	return &StreamUploadResult{
		UID:               uid,
		ScheduledDeletion: scheduledDeletion,
	}, nil
}

func (c *StreamClient) uploadChunks(location string, src io.Reader, size int64) error {
	var offset int64
	for offset < size {
		remaining := size - offset
		chunkSize := c.chunkSize
		if remaining < chunkSize {
			chunkSize = remaining
		}
		body := io.LimitReader(src, chunkSize)

		req, err := http.NewRequest(http.MethodPatch, location, body)
		if err != nil {
			return fmt.Errorf("building PATCH at offset %d: %w", offset, err)
		}
		req.Header.Set("Authorization", "Bearer "+c.cfg.APIToken)
		req.Header.Set("Tus-Resumable", "1.0.0")
		req.Header.Set("Content-Type", "application/offset+octet-stream")
		req.Header.Set("Upload-Offset", strconv.FormatInt(offset, 10))
		req.ContentLength = chunkSize

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return fmt.Errorf("PATCH at offset %d: %w", offset, err)
		}

		if resp.StatusCode != http.StatusNoContent {
			respBody, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			return fmt.Errorf("PATCH at offset %d returned %d: %s", offset, resp.StatusCode, respBody)
		}
		resp.Body.Close()
		offset += chunkSize
	}
	return nil
}

// encodeUploadMetadata produces a TUS Upload-Metadata header value: a comma-
// separated list of "key base64-of-value" entries. CF Stream's Upload-Metadata
// keys are lowercase per the API doc.
func encodeUploadMetadata(kv map[string]string) string {
	parts := make([]string, 0, len(kv))
	for k, v := range kv {
		enc := base64.StdEncoding.EncodeToString([]byte(v))
		parts = append(parts, k+" "+enc)
	}
	return strings.Join(parts, ",")
}
