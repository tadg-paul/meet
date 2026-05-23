// ABOUTME: Regression tests for the meet HTTP server.
// ABOUTME: Exercises routes and responses through the public HTTP interface.

package regression

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"io"
	"strings"
	"testing"

	"github.com/tadg-paul/meet/internal/server"
)

func newTestServer() *httptest.Server {
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	srv := server.New(server.Config{
		Addr:    "127.0.0.1:0",
		BaseURL: "https://meet.lobb.ie",
		AppID:   "vpaas-magic-cookie-test",
		Logger:  logger,
	})
	return httptest.NewServer(srv.Handler())
}

func TestHealthEndpoint(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/health")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("health status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
}

// The historical "root serves default room" behaviour was removed in #7.
// "/" now 404s when the room registry is wired; the canonical coverage of
// that behaviour lives in TestGate_RootPathRejected_RT7_27 (registry_gate_test.go).

func TestRoomPageRendered(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/my-meeting")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("room status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	rawBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read body: %v", err)
	}
	body := string(rawBody)

	if !strings.Contains(body, "my-meeting") {
		t.Error("room page does not contain room name")
	}
	if !strings.Contains(body, "vpaas-magic-cookie-test/my-meeting") {
		t.Error("room page does not contain full JaaS room path")
	}
	if !strings.Contains(body, `<span class="domain-highlight">meet</span>`) {
		t.Error("room page does not contain highlighted domain part")
	}
	if !strings.Contains(body, `<span class="domain-dim">.lobb.ie</span>`) {
		t.Error("room page does not contain dim domain part")
	}
}

func TestNestedPathRejected(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/some/nested/path")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("nested path status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
}

func TestFontServed(t *testing.T) {
	ts := newTestServer()
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/static/SpecialElite-Regular.woff2")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("font status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
}
