// ABOUTME: Regression tests for the server-side room registry gate (issue #7).
// ABOUTME: Covers AC7.1 (unregistered → no meeting page), AC7.2 (time-window
// ABOUTME: enforcement), AC7.6 (moderator-JWT bypass).

package regression

import (
	"crypto/rand"
	"crypto/rsa"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/tadg-paul/meet/internal/server"
)

// fixture builds a Server backed by a fresh RoomsLog and a controllable clock.
type fixture struct {
	ts      *httptest.Server
	rooms   *server.RoomsLog
	now     time.Time
	privKey *rsa.PrivateKey
}

func newGateFixture(t *testing.T) *fixture {
	t.Helper()
	dir := t.TempDir()
	rooms, err := server.NewRoomsLog(dir)
	if err != nil {
		t.Fatalf("NewRoomsLog: %v", err)
	}
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey: %v", err)
	}
	fx := &fixture{
		rooms:   rooms,
		now:     mustRFC3339(t, "2026-05-22T20:00:00Z"),
		privKey: priv,
	}
	srv := server.New(server.Config{
		Addr:         "127.0.0.1:0",
		BaseURL:      "https://meet.lobb.ie",
		AppID:        "vpaas-magic-cookie-test",
		Logger:       slog.New(slog.NewTextHandler(os.Stderr, nil)),
		Rooms:        rooms,
		JWTPublicKey: &priv.PublicKey,
		Now:          func() time.Time { return fx.now },
	})
	fx.ts = httptest.NewServer(srv.Handler())
	t.Cleanup(fx.ts.Close)
	return fx
}

func (f *fixture) get(t *testing.T, path string) (status int, body string) {
	t.Helper()
	resp, err := http.Get(f.ts.URL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

// moderatorJWT returns a query-param value for ?jwt= that the gate accepts.
// Uses real wall-clock time for exp/nbf/iat because jwt.Parse validates those
// against time.Now, not the server's injected clock.
func (f *fixture) moderatorJWT(t *testing.T) string {
	t.Helper()
	now := time.Now()
	claims := jwt.MapClaims{
		"aud":  "jitsi",
		"iss":  "chat",
		"sub":  "vpaas-magic-cookie-test",
		"room": "*",
		"iat":  now.Unix(),
		"nbf":  now.Add(-time.Minute).Unix(),
		"exp":  now.Add(2 * time.Hour).Unix(),
		"context": map[string]any{
			"user": map[string]any{
				"name":      "Moderator",
				"moderator": "true",
			},
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	signed, err := tok.SignedString(f.privKey)
	if err != nil {
		t.Fatalf("sign jwt: %v", err)
	}
	return signed
}

func (f *fixture) registerRoom(t *testing.T, room string, validFrom, validUntil time.Time) {
	t.Helper()
	err := f.rooms.Append(server.RoomLogEntry{
		Timestamp:  f.now,
		Room:       room,
		Status:     server.RoomCreated,
		ValidFrom:  validFrom,
		ValidUntil: validUntil,
	})
	if err != nil {
		t.Fatalf("register %s: %v", room, err)
	}
}

func (f *fixture) cancelRoom(t *testing.T, room string) {
	t.Helper()
	err := f.rooms.Append(server.RoomLogEntry{
		Timestamp: f.now,
		Room:      room,
		Status:    server.RoomCancelled,
	})
	if err != nil {
		t.Fatalf("cancel %s: %v", room, err)
	}
}

func bodyIsMeetingPage(body string) bool {
	return strings.Contains(body, `<div id="jaas-container"></div>`)
}

// AC7.1 — unregistered room: not a meeting page, status != 200.
func TestGate_UnregisteredRoom_NoMeetingPage(t *testing.T) {
	f := newGateFixture(t)

	status, body := f.get(t, "/never-registered-room")

	if status == http.StatusOK {
		t.Errorf("unregistered room returned %d, want non-200", status)
	}
	if bodyIsMeetingPage(body) {
		t.Errorf("unregistered room served meeting page; body=%q", body[:min(120, len(body))])
	}
}

// AC7.1 — unregistered room: no JaaS roomName, no AppID embedded.
func TestGate_UnregisteredRoom_NoJaaSPayload(t *testing.T) {
	f := newGateFixture(t)

	_, body := f.get(t, "/some-random-name")

	if strings.Contains(body, "vpaas-magic-cookie-test/some-random-name") {
		t.Error("unregistered room body contains JaaS roomName template")
	}
	if strings.Contains(body, "JitsiMeetExternalAPI") {
		t.Error("unregistered room body contains JaaS init JS")
	}
}

// AC7.2 — registered, before its window: no meeting page.
func TestGate_BeforeWindow(t *testing.T) {
	f := newGateFixture(t)
	f.registerRoom(t, "foo",
		mustRFC3339(t, "2026-05-22T21:00:00Z"),
		mustRFC3339(t, "2026-05-22T23:00:00Z"),
	)
	// fixture clock is 2026-05-22T20:00:00Z, before valid_from
	status, body := f.get(t, "/foo")
	if status == http.StatusOK {
		t.Errorf("before-window returned %d, want non-200", status)
	}
	if bodyIsMeetingPage(body) {
		t.Error("before-window served meeting page")
	}
}

// AC7.2 — registered, during its window: meeting page returns.
func TestGate_DuringWindow(t *testing.T) {
	f := newGateFixture(t)
	f.registerRoom(t, "foo",
		mustRFC3339(t, "2026-05-22T19:00:00Z"),
		mustRFC3339(t, "2026-05-22T21:00:00Z"),
	)
	status, body := f.get(t, "/foo")
	if status != http.StatusOK {
		t.Errorf("during-window returned %d, want 200", status)
	}
	if !bodyIsMeetingPage(body) {
		t.Errorf("during-window did not serve meeting page; body=%q", body[:min(200, len(body))])
	}
}

// AC7.2 — registered, after its window: no meeting page.
func TestGate_AfterWindow(t *testing.T) {
	f := newGateFixture(t)
	f.registerRoom(t, "foo",
		mustRFC3339(t, "2026-05-22T18:00:00Z"),
		mustRFC3339(t, "2026-05-22T19:00:00Z"),
	)
	status, body := f.get(t, "/foo")
	if status == http.StatusOK {
		t.Errorf("after-window returned %d, want non-200", status)
	}
	if bodyIsMeetingPage(body) {
		t.Error("after-window served meeting page")
	}
}

// AC7.2 boundary — at exactly valid_from: meeting page returns (inclusive).
func TestGate_AtValidFromBoundary(t *testing.T) {
	f := newGateFixture(t)
	f.registerRoom(t, "foo", f.now, f.now.Add(time.Hour))
	status, _ := f.get(t, "/foo")
	if status != http.StatusOK {
		t.Errorf("at valid_from returned %d, want 200 (inclusive)", status)
	}
}

// AC7.2 boundary — at exactly valid_until: meeting page returns (inclusive).
func TestGate_AtValidUntilBoundary(t *testing.T) {
	f := newGateFixture(t)
	f.registerRoom(t, "foo", f.now.Add(-time.Hour), f.now)
	status, _ := f.get(t, "/foo")
	if status != http.StatusOK {
		t.Errorf("at valid_until returned %d, want 200 (inclusive)", status)
	}
}

// AC7.4 + AC7.2 — registered then cancelled, still inside its original window:
// no meeting page.
func TestGate_CancelledRoom_NoMeetingPage(t *testing.T) {
	f := newGateFixture(t)
	f.registerRoom(t, "foo",
		mustRFC3339(t, "2026-05-22T19:00:00Z"),
		mustRFC3339(t, "2026-05-22T21:00:00Z"),
	)
	f.cancelRoom(t, "foo")
	status, body := f.get(t, "/foo")
	if status == http.StatusOK {
		t.Errorf("cancelled room returned %d, want non-200", status)
	}
	if bodyIsMeetingPage(body) {
		t.Error("cancelled room served meeting page")
	}
}

// AC7.6 — valid moderator JWT on unregistered room: meeting page returns.
func TestGate_ModeratorJWT_UnregisteredRoom(t *testing.T) {
	f := newGateFixture(t)
	jwtStr := f.moderatorJWT(t)
	status, body := f.get(t, "/never-heard-of-this-room?jwt="+jwtStr)
	if status != http.StatusOK {
		t.Errorf("moderator on unregistered room returned %d, want 200", status)
	}
	if !bodyIsMeetingPage(body) {
		t.Error("moderator on unregistered room did not serve meeting page")
	}
}

// AC7.6 — valid moderator JWT on cancelled room: meeting page returns.
func TestGate_ModeratorJWT_CancelledRoom(t *testing.T) {
	f := newGateFixture(t)
	f.registerRoom(t, "foo",
		mustRFC3339(t, "2026-05-22T19:00:00Z"),
		mustRFC3339(t, "2026-05-22T21:00:00Z"),
	)
	f.cancelRoom(t, "foo")
	jwtStr := f.moderatorJWT(t)
	status, _ := f.get(t, "/foo?jwt="+jwtStr)
	if status != http.StatusOK {
		t.Errorf("moderator on cancelled room returned %d, want 200", status)
	}
}

// AC7.6 — valid moderator JWT outside the room's window: meeting page returns.
func TestGate_ModeratorJWT_OutOfWindow(t *testing.T) {
	f := newGateFixture(t)
	f.registerRoom(t, "foo",
		mustRFC3339(t, "2026-05-22T18:00:00Z"),
		mustRFC3339(t, "2026-05-22T19:00:00Z"),
	)
	jwtStr := f.moderatorJWT(t)
	status, _ := f.get(t, "/foo?jwt="+jwtStr)
	if status != http.StatusOK {
		t.Errorf("moderator out-of-window returned %d, want 200", status)
	}
}

// AC7.6 (negative) — garbage JWT is treated as no JWT.
func TestGate_InvalidJWT_TreatedAsNoJWT(t *testing.T) {
	f := newGateFixture(t)
	// Unregistered room + invalid JWT → not allowed.
	status, _ := f.get(t, "/unregistered?jwt=this.is.not.a.real.jwt")
	if status == http.StatusOK {
		t.Errorf("invalid JWT on unregistered room returned %d, want non-200", status)
	}
}

// AC7.6 (negative) — JWT signed by the wrong key is rejected.
func TestGate_WrongKeyJWT_Rejected(t *testing.T) {
	f := newGateFixture(t)
	other, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey: %v", err)
	}
	now := time.Now()
	claims := jwt.MapClaims{
		"context": map[string]any{
			"user": map[string]any{"moderator": "true"},
		},
		"exp": now.Add(time.Hour).Unix(),
		"iat": now.Unix(),
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	signed, err := tok.SignedString(other)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	status, _ := f.get(t, "/unregistered?jwt="+signed)
	if status == http.StatusOK {
		t.Errorf("wrong-key JWT on unregistered room returned %d, want non-200", status)
	}
}

// AC7.6 (negative) — non-moderator JWT is rejected for bypass.
func TestGate_NonModeratorJWT_Rejected(t *testing.T) {
	f := newGateFixture(t)
	now := time.Now()
	claims := jwt.MapClaims{
		"context": map[string]any{
			"user": map[string]any{"moderator": "false"},
		},
		"exp": now.Add(time.Hour).Unix(),
		"iat": now.Unix(),
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	signed, err := tok.SignedString(f.privKey)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	status, _ := f.get(t, "/unregistered?jwt="+signed)
	if status == http.StatusOK {
		t.Errorf("non-moderator JWT on unregistered room returned %d, want non-200", status)
	}
}

// "/" with empty path and no JWT: 404 — no implicit default room (#7 drops it).
func TestGate_RootPathRejected(t *testing.T) {
	f := newGateFixture(t)
	status, _ := f.get(t, "/")
	if status == http.StatusOK {
		t.Errorf("/ returned %d, want non-200 (no implicit default room)", status)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
