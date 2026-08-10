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
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/tigger-developer/meet/internal/server"
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
	return f.moderatorJWTForRoom(t, "*")
}

func (f *fixture) moderatorJWTForRoom(t *testing.T, room string) string {
	t.Helper()
	now := time.Now()
	claims := jwt.MapClaims{
		"aud":  "jitsi",
		"iss":  "chat",
		"sub":  "vpaas-magic-cookie-test",
		"room": room,
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
func TestGate_UnregisteredRoom_NoMeetingPage_RT7_1(t *testing.T) {
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
func TestGate_UnregisteredRoom_NoJaaSPayload_RT7_2(t *testing.T) {
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
func TestGate_BeforeWindow_RT7_3(t *testing.T) {
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
func TestGate_DuringWindow_RT7_4(t *testing.T) {
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
func TestGate_AfterWindow_RT7_5(t *testing.T) {
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
func TestGate_AtValidFromBoundary_RT7_6(t *testing.T) {
	f := newGateFixture(t)
	f.registerRoom(t, "foo", f.now, f.now.Add(time.Hour))
	status, _ := f.get(t, "/foo")
	if status != http.StatusOK {
		t.Errorf("at valid_from returned %d, want 200 (inclusive)", status)
	}
}

// AC7.2 boundary — at exactly valid_until: meeting page returns (inclusive).
func TestGate_AtValidUntilBoundary_RT7_7(t *testing.T) {
	f := newGateFixture(t)
	f.registerRoom(t, "foo", f.now.Add(-time.Hour), f.now)
	status, _ := f.get(t, "/foo")
	if status != http.StatusOK {
		t.Errorf("at valid_until returned %d, want 200 (inclusive)", status)
	}
}

// AC7.4 + AC7.2 — registered then cancelled, still inside its original window:
// no meeting page.
func TestGate_CancelledRoom_NoMeetingPage_RT7_13(t *testing.T) {
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
func TestGate_ModeratorJWT_UnregisteredRoom_RT7_19(t *testing.T) {
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
func TestGate_ModeratorJWT_CancelledRoom_RT7_20(t *testing.T) {
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
func TestGate_ModeratorJWT_OutOfWindow_RT7_21(t *testing.T) {
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
func TestGate_InvalidJWT_TreatedAsNoJWT_RT7_22(t *testing.T) {
	f := newGateFixture(t)
	// Unregistered room + invalid JWT → not allowed.
	status, _ := f.get(t, "/unregistered?jwt=this.is.not.a.real.jwt")
	if status == http.StatusOK {
		t.Errorf("invalid JWT on unregistered room returned %d, want non-200", status)
	}
}

// AC7.6 (negative) — JWT signed by the wrong key is rejected.
func TestGate_WrongKeyJWT_Rejected_RT7_25(t *testing.T) {
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
func TestGate_NonModeratorJWT_Rejected_RT7_26(t *testing.T) {
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
func TestGate_RootPathRejected_RT7_27(t *testing.T) {
	f := newGateFixture(t)
	status, _ := f.get(t, "/")
	if status == http.StatusOK {
		t.Errorf("/ returned %d, want non-200 (no implicit default room)", status)
	}
}

// AC12.4 — latest created row for a reused room controls guest access.
func TestGate_ReusedRoomLatestActiveWindow_RT12_16_RT12_17(t *testing.T) {
	f := newGateFixture(t)
	f.registerRoom(t, "reused",
		mustRFC3339(t, "2026-05-20T19:00:00Z"),
		mustRFC3339(t, "2026-05-20T21:00:00Z"),
	)
	f.registerRoom(t, "reused",
		mustRFC3339(t, "2026-05-22T19:00:00Z"),
		mustRFC3339(t, "2026-05-22T21:00:00Z"),
	)
	status, body := f.get(t, "/reused")
	if status != http.StatusOK || !bodyIsMeetingPage(body) {
		t.Fatalf("latest active window status=%d meeting=%v", status, bodyIsMeetingPage(body))
	}
	f.now = mustRFC3339(t, "2026-05-22T22:00:00Z")
	status, _ = f.get(t, "/reused")
	if status == http.StatusOK {
		t.Errorf("outside latest window returned %d, want non-200", status)
	}
}

// AC12.4 — a latest cancelled row overrides earlier active rows.
func TestGate_ReusedRoomLatestCancelled_RT12_18(t *testing.T) {
	f := newGateFixture(t)
	f.registerRoom(t, "reused",
		mustRFC3339(t, "2026-05-22T19:00:00Z"),
		mustRFC3339(t, "2026-05-22T21:00:00Z"),
	)
	f.cancelRoom(t, "reused")
	status, _ := f.get(t, "/reused")
	if status == http.StatusOK {
		t.Errorf("latest cancelled row returned %d, want non-200", status)
	}
}

// AC12.4 — moderator authorization does not make the plain guest URL joinable.
func TestGate_ModeratorAuthDoesNotOpenPlainRoom_RT12_19(t *testing.T) {
	f := newGateFixture(t)
	f.registerRoom(t, "plain",
		mustRFC3339(t, "2026-05-22T18:00:00Z"),
		mustRFC3339(t, "2026-05-22T19:00:00Z"),
	)
	status, _ := f.get(t, "/plain")
	if status == http.StatusOK {
		t.Errorf("plain out-of-window room returned %d, want non-200", status)
	}
}

// AC12.7 — wildcard super-moderator JWTs still bypass any room during lifetime.
func TestGate_SuperModeratorWildcardAllowsDifferentRoom_RT12_29(t *testing.T) {
	f := newGateFixture(t)
	jwtStr := f.moderatorJWTForRoom(t, "*")
	status, body := f.get(t, "/different-room?jwt="+jwtStr)
	if status != http.StatusOK || !bodyIsMeetingPage(body) {
		t.Fatalf("wildcard moderator status=%d meeting=%v", status, bodyIsMeetingPage(body))
	}
}

// AC12.8 — room-scoped moderator JWTs do not bypass other rooms.
func TestGate_RoomScopedModeratorRejectsDifferentRoom_RT12_32(t *testing.T) {
	f := newGateFixture(t)
	jwtStr := f.moderatorJWTForRoom(t, "allowed-room")
	status, _ := f.get(t, "/different-room?jwt="+jwtStr)
	if status == http.StatusOK {
		t.Errorf("room-scoped moderator on different room returned %d, want non-200", status)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// --- Issue #13: inactive-room page for gated meeting slugs ---

const inactiveParagraph = "This meeting room is not active. If you have been given this link for a meeting, it may be the case that this is the correct meeting room but the room is not active currently. Please check the meeting date and time."

func bodyIsInactivePage(body string) bool {
	return strings.Contains(body, "<h1>Inactive</h1>") && strings.Contains(body, inactiveParagraph)
}

// doReq issues a request with an explicit method and returns the response
// (status and headers) plus the body. The body is fully read and closed.
func (f *fixture) doReq(t *testing.T, method, path string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(method, f.ts.URL+path, nil)
	if err != nil {
		t.Fatalf("new request %s %s: %v", method, path, err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, string(raw)
}

// headerFingerprint renders a stable, comparable string of response headers,
// excluding headers that legitimately vary between two otherwise-identical
// responses (Date is per-request; Content-Length is compared separately via
// resp.ContentLength to avoid client-side header normalization differences).
func headerFingerprint(h http.Header) string {
	keys := make([]string, 0, len(h))
	for k := range h {
		if k == "Date" || k == "Content-Length" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteString(": ")
		b.WriteString(strings.Join(h[k], ","))
		b.WriteString("\n")
	}
	return b.String()
}

// blockedResponse captures the observable surface of a blocked-slug response.
type blockedResponse struct {
	status      int
	fingerprint string
	length      int64
	body        string
}

func (f *fixture) blocked(t *testing.T, path string) blockedResponse {
	t.Helper()
	resp, body := f.doReq(t, "GET", path)
	return blockedResponse{
		status:      resp.StatusCode,
		fingerprint: headerFingerprint(resp.Header),
		length:      resp.ContentLength,
		body:        body,
	}
}

// AC13.1 / RT-13.1 — unregistered slug: 404 inactive page.
func TestInactive_UnregisteredSlug_RT13_1(t *testing.T) {
	f := newGateFixture(t)
	resp, body := f.doReq(t, "GET", "/never-registered-room")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
	if !bodyIsInactivePage(body) {
		t.Errorf("body is not the inactive page; got %q", body[:min(200, len(body))])
	}
}

// AC13.1 / RT-13.2 — registered but before its window: same inactive page.
func TestInactive_BeforeWindow_RT13_2(t *testing.T) {
	f := newGateFixture(t)
	f.registerRoom(t, "foo", f.now.Add(time.Hour), f.now.Add(2*time.Hour))
	resp, body := f.doReq(t, "GET", "/foo")
	if resp.StatusCode != http.StatusNotFound || !bodyIsInactivePage(body) {
		t.Errorf("before-window: status=%d inactive=%v", resp.StatusCode, bodyIsInactivePage(body))
	}
}

// AC13.1 / RT-13.3 — registered but after its window: same inactive page.
func TestInactive_AfterWindow_RT13_3(t *testing.T) {
	f := newGateFixture(t)
	f.registerRoom(t, "foo", f.now.Add(-2*time.Hour), f.now.Add(-time.Hour))
	resp, body := f.doReq(t, "GET", "/foo")
	if resp.StatusCode != http.StatusNotFound || !bodyIsInactivePage(body) {
		t.Errorf("after-window: status=%d inactive=%v", resp.StatusCode, bodyIsInactivePage(body))
	}
}

// AC13.1 / RT-13.4 — cancelled latest row: same inactive page.
func TestInactive_Cancelled_RT13_4(t *testing.T) {
	f := newGateFixture(t)
	f.registerRoom(t, "foo", f.now.Add(-time.Hour), f.now.Add(time.Hour))
	f.cancelRoom(t, "foo")
	resp, body := f.doReq(t, "GET", "/foo")
	if resp.StatusCode != http.StatusNotFound || !bodyIsInactivePage(body) {
		t.Errorf("cancelled: status=%d inactive=%v", resp.StatusCode, bodyIsInactivePage(body))
	}
}

// AC13.1 / RT-13.5 — all four blocked states are byte-for-byte identical in
// status, headers, and body.
func TestInactive_BlockedStatesIdentical_RT13_5(t *testing.T) {
	f := newGateFixture(t)
	f.registerRoom(t, "before", f.now.Add(time.Hour), f.now.Add(2*time.Hour))
	f.registerRoom(t, "after", f.now.Add(-2*time.Hour), f.now.Add(-time.Hour))
	f.registerRoom(t, "cxl", f.now.Add(-time.Hour), f.now.Add(time.Hour))
	f.cancelRoom(t, "cxl")

	unknown := f.blocked(t, "/never-registered")
	before := f.blocked(t, "/before")
	after := f.blocked(t, "/after")
	cancelled := f.blocked(t, "/cxl")

	for _, got := range []blockedResponse{before, after, cancelled} {
		if got.status != unknown.status {
			t.Errorf("status differs: %d vs %d", got.status, unknown.status)
		}
		if got.fingerprint != unknown.fingerprint {
			t.Errorf("headers differ:\n%s\nvs\n%s", got.fingerprint, unknown.fingerprint)
		}
		if got.length != unknown.length {
			t.Errorf("content-length differs: %d vs %d", got.length, unknown.length)
		}
		if got.body != unknown.body {
			t.Errorf("body differs across blocked states")
		}
	}
}

// AC13.1 / RT-13.6 — blocked slug carries no JaaS meeting embed.
func TestInactive_NoMeetingEmbed_RT13_6(t *testing.T) {
	f := newGateFixture(t)
	_, body := f.doReq(t, "GET", "/blocked-slug")
	if bodyIsMeetingPage(body) {
		t.Error("blocked slug served meeting page")
	}
	for _, marker := range []string{"JitsiMeetExternalAPI", "vpaas-magic-cookie-test", "jaas-container"} {
		if strings.Contains(body, marker) {
			t.Errorf("blocked slug body contains embed marker %q", marker)
		}
	}
}

// AC13.1 / RT-13.12 — HEAD and a disallowed method match the GET inactive
// response, with no header that varies by blocked state.
func TestInactive_HeadAndMethodParity_RT13_12(t *testing.T) {
	f := newGateFixture(t)
	f.registerRoom(t, "after", f.now.Add(-2*time.Hour), f.now.Add(-time.Hour))

	get, getBody := f.doReq(t, "GET", "/unknown")

	head, _ := f.doReq(t, "HEAD", "/unknown")
	if head.StatusCode != get.StatusCode {
		t.Errorf("HEAD status=%d, want %d", head.StatusCode, get.StatusCode)
	}
	if head.Header.Get("Content-Type") != get.Header.Get("Content-Type") {
		t.Errorf("HEAD Content-Type=%q, want %q", head.Header.Get("Content-Type"), get.Header.Get("Content-Type"))
	}
	if head.Header.Get("Cache-Control") != get.Header.Get("Cache-Control") {
		t.Errorf("HEAD Cache-Control=%q, want %q", head.Header.Get("Cache-Control"), get.Header.Get("Cache-Control"))
	}
	if head.ContentLength != int64(len(getBody)) {
		t.Errorf("HEAD ContentLength=%d, want %d", head.ContentLength, len(getBody))
	}

	// HEAD must not vary by blocked state.
	headKnown, _ := f.doReq(t, "HEAD", "/after")
	if headKnown.ContentLength != head.ContentLength ||
		headKnown.Header.Get("Cache-Control") != head.Header.Get("Cache-Control") ||
		headKnown.Header.Get("Content-Type") != head.Header.Get("Content-Type") {
		t.Error("HEAD headers vary by blocked state")
	}

	// A disallowed method returns the same status and headers as GET.
	put, _ := f.doReq(t, "PUT", "/unknown")
	if put.StatusCode != get.StatusCode {
		t.Errorf("PUT status=%d, want %d", put.StatusCode, get.StatusCode)
	}
	if headerFingerprint(put.Header) != headerFingerprint(get.Header) {
		t.Error("PUT headers differ from GET inactive response")
	}
}

// AC13.1 / RT-13.13 — the empty top-level segment "/" returns the inactive page.
func TestInactive_RootPath_RT13_13(t *testing.T) {
	f := newGateFixture(t)
	resp, body := f.doReq(t, "GET", "/")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("/ status=%d, want 404", resp.StatusCode)
	}
	if !bodyIsInactivePage(body) {
		t.Errorf("/ did not return the inactive page; got %q", body[:min(200, len(body))])
	}
}

// RT-13.7 removed: superseded by #14. The moderator route is now active-gated
// (an inactive room returns the guest inactive 404), so the old "always shows
// the form" assertion no longer holds. See moderator_gate_test.go (RT-14.1).

// AC13.2 / RT-13.8 — a nested invalid room path keeps its response.
func TestInactive_NestedInvalidPath_RT13_8(t *testing.T) {
	f := newGateFixture(t)
	resp, body := f.doReq(t, "GET", "/bad/name")
	if resp.StatusCode == http.StatusOK {
		t.Errorf("nested path status=%d, want non-200", resp.StatusCode)
	}
	if strings.Contains(body, inactiveParagraph) {
		t.Error("nested invalid path contains inactive-room message")
	}
}

// AC13.2 / RT-13.9 — a nested moderator-like path does not become an inactive page.
func TestInactive_NestedModeratorLikePath_RT13_9(t *testing.T) {
	f := newGateFixture(t)
	_, body := f.doReq(t, "GET", "/bad/name/moderator")
	if strings.Contains(body, inactiveParagraph) {
		t.Error("nested moderator-like path contains inactive-room message")
	}
	if bodyIsInactivePage(body) {
		t.Error("nested moderator-like path rendered the inactive page")
	}
}

// RT-13.14 removed: superseded by #14. The moderator route now varies with
// registry state by design (active -> form, inactive -> the guest 404), which
// leaks no more than the guest gate already does. See moderator_gate_test.go
// (RT-14.4: unregistered and inactive-registered are indistinguishable).

// AC13.3 / RT-13.10 — an active room still serves the meeting page.
func TestInactive_ActiveRoomServesMeeting_RT13_10(t *testing.T) {
	f := newGateFixture(t)
	f.registerRoom(t, "live", f.now.Add(-time.Hour), f.now.Add(time.Hour))
	resp, body := f.doReq(t, "GET", "/live")
	if resp.StatusCode != http.StatusOK || !bodyIsMeetingPage(body) {
		t.Errorf("active room status=%d meeting=%v", resp.StatusCode, bodyIsMeetingPage(body))
	}
	if bodyIsInactivePage(body) {
		t.Error("active room rendered the inactive page")
	}
}

// AC13.3 / RT-13.11 — a previously-valid (cancelled or expired) slug is
// indistinguishable from a never-registered slug.
func TestInactive_PreviouslyValidIndistinguishable_RT13_11(t *testing.T) {
	f := newGateFixture(t)
	f.registerRoom(t, "expired", f.now.Add(-2*time.Hour), f.now.Add(-time.Hour))
	f.registerRoom(t, "cxl", f.now.Add(-time.Hour), f.now.Add(time.Hour))
	f.cancelRoom(t, "cxl")

	unknown := f.blocked(t, "/never-registered")
	for name, got := range map[string]blockedResponse{
		"expired":   f.blocked(t, "/expired"),
		"cancelled": f.blocked(t, "/cxl"),
	} {
		if got.status != unknown.status || got.fingerprint != unknown.fingerprint ||
			got.length != unknown.length || got.body != unknown.body {
			t.Errorf("%s slug distinguishable from never-registered slug", name)
		}
	}
}

// AC13.4 / RT-13.15 — the inactive-room response is not cacheable.
func TestInactive_NoStore_RT13_15(t *testing.T) {
	f := newGateFixture(t)
	resp, _ := f.doReq(t, "GET", "/blocked-slug")
	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("inactive Cache-Control=%q, want no-store", got)
	}
}

// AC13.4 / RT-13.16 — the meeting page is not cacheable.
func TestInactive_MeetingNoStore_RT13_16(t *testing.T) {
	f := newGateFixture(t)
	f.registerRoom(t, "live", f.now.Add(-time.Hour), f.now.Add(time.Hour))
	resp, body := f.doReq(t, "GET", "/live")
	if !bodyIsMeetingPage(body) {
		t.Fatalf("precondition: /live is not the meeting page")
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("meeting Cache-Control=%q, want no-store", got)
	}
}
