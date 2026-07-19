// ABOUTME: Regression tests for room-scoped moderator magic links (issue #12).
// ABOUTME: Exercises the HTTP moderator request, verifier, and room gate flows.

package regression

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/tigger-developer/meet/internal/server"
)

type captureModeratorMailer struct {
	messages []capturedModeratorMessage
	fail     bool
}

type capturedModeratorMessage struct {
	to   string
	link string
}

func (m *captureModeratorMailer) SendModeratorMagicLink(to, link string) error {
	if m.fail {
		return errCaptureMailerFailure{}
	}
	m.messages = append(m.messages, capturedModeratorMessage{to: to, link: link})
	return nil
}

type errCaptureMailerFailure struct{}

func (errCaptureMailerFailure) Error() string {
	return "capture mailer failure"
}

type moderatorFixture struct {
	ts     *httptest.Server
	rooms  *server.RoomsLog
	links  *server.ModeratorLinksLog
	mailer *captureModeratorMailer
	key    *rsa.PrivateKey
	logs   *bytes.Buffer
	now    time.Time
}

func newModeratorFixture(t *testing.T, configure func(*server.Config, *captureModeratorMailer)) *moderatorFixture {
	t.Helper()
	dir := t.TempDir()
	rooms, err := server.NewRoomsLog(dir)
	if err != nil {
		t.Fatalf("NewRoomsLog: %v", err)
	}
	links, err := server.NewModeratorLinksLog(dir)
	if err != nil {
		t.Fatalf("NewModeratorLinksLog: %v", err)
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	logs := &bytes.Buffer{}
	mailer := &captureModeratorMailer{}
	f := &moderatorFixture{rooms: rooms, links: links, mailer: mailer, key: key, logs: logs, now: mustRFC3339(t, "2026-05-22T20:00:00Z")}
	cfg := server.Config{
		BaseURL:      "https://meet.lobb.ie",
		AppID:        "vpaas-magic-cookie-test",
		Logger:       slog.New(slog.NewJSONHandler(logs, nil)),
		Rooms:        rooms,
		JWTPublicKey: &key.PublicKey,
		Now:          func() time.Time { return f.now },
		ModeratorAuth: server.ModeratorAuthConfig{
			Enabled:         true,
			MagicLinkTTL:    15 * time.Minute,
			ModeratorJWTTTL: 2 * time.Hour,
			SigningKey:      []byte("test moderator auth signing key"),
			Rooms: map[string][]string{
				"allowed-room": []string{"mod@example.com", "Second@Example.com"},
				"other-room":   []string{"other@example.com"},
				"workshop":     []string{"mod@example.com"},
			},
			Links:  links,
			Mailer: mailer,
		},
		JWTPrivateKey: key,
		JWTKeyID:      "test-key",
	}
	if configure != nil {
		configure(&cfg, mailer)
	}
	srv := server.New(cfg)
	f.ts = httptest.NewServer(srv.Handler())
	t.Cleanup(f.ts.Close)
	return f
}

func (f *moderatorFixture) get(t *testing.T, path string) (int, string, http.Header) {
	t.Helper()
	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Get(f.ts.URL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw), resp.Header
}

func (f *moderatorFixture) postForm(t *testing.T, path string, values url.Values) (int, string) {
	t.Helper()
	resp, err := http.PostForm(f.ts.URL+path, values)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

func extractModeratorToken(t *testing.T, location string) string {
	t.Helper()
	u, err := url.Parse(location)
	if err != nil {
		t.Fatalf("parse redirect location: %v", err)
	}
	token := u.Query().Get("jwt")
	if token == "" {
		t.Fatalf("redirect location has no jwt: %q", location)
	}
	return token
}

// AC12.1, AC12.6 — authorised path-room/email pairs receive the generic page
// and an outbound magic-link message.
func TestModeratorAuth_AllowedEmailSendsMagicLink_RT12_1_RT12_24(t *testing.T) {
	f := newModeratorFixture(t, nil)
	status, body := f.postForm(t, "/allowed-room/moderator", url.Values{"email": {"mod@example.com"}})
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%q", status, body)
	}
	if !strings.Contains(body, "check your email") {
		t.Errorf("body=%q, want generic check-email page", body)
	}
	if len(f.mailer.messages) != 1 {
		t.Fatalf("messages=%d, want 1", len(f.mailer.messages))
	}
	if f.mailer.messages[0].to != "mod@example.com" {
		t.Errorf("to=%q", f.mailer.messages[0].to)
	}
	if !strings.Contains(f.mailer.messages[0].link, "/allowed-room/moderator/verify?token=") {
		t.Errorf("link=%q, want room-scoped verify URL", f.mailer.messages[0].link)
	}
}

// AC12.1 — unauthorised and unknown addresses get the same generic response
// without outbound delivery.
func TestModeratorAuth_UnauthorisedEmailsDoNotSend_RT12_2_RT12_3(t *testing.T) {
	for _, tc := range []struct {
		name  string
		path  string
		email string
	}{
		{"wrong-room", "/other-room/moderator", "mod@example.com"},
		{"unknown-email", "/allowed-room/moderator", "unknown@example.com"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newModeratorFixture(t, nil)
			status, body := f.postForm(t, tc.path, url.Values{"email": {tc.email}})
			if status != http.StatusOK {
				t.Fatalf("status=%d body=%q", status, body)
			}
			if !strings.Contains(body, "check your email") {
				t.Errorf("body=%q, want generic check-email page", body)
			}
			if len(f.mailer.messages) != 0 {
				t.Errorf("messages=%d, want 0", len(f.mailer.messages))
			}
		})
	}
}

// AC12.1, AC12.6 — malformed emails and invalid room routes do not deliver.
func TestModeratorAuth_InvalidInputsRejected_RT12_4_RT12_5_RT12_26(t *testing.T) {
	f := newModeratorFixture(t, nil)
	status, _ := f.postForm(t, "/allowed-room/moderator", url.Values{"email": {"not-an-email"}})
	if status != http.StatusBadRequest {
		t.Errorf("invalid email status=%d, want 400", status)
	}
	status, _, _ = f.get(t, "/bad/name/moderator")
	if status == http.StatusOK {
		t.Errorf("nested moderator path returned %d, want non-200", status)
	}
	if len(f.mailer.messages) != 0 {
		t.Errorf("messages=%d, want 0", len(f.mailer.messages))
	}
}

// AC12.6 — the form is scoped by URL path, not by form-supplied room data.
func TestModeratorAuth_FormRoomIgnored_RT12_25(t *testing.T) {
	f := newModeratorFixture(t, nil)
	status, _ := f.postForm(t, "/workshop/moderator", url.Values{
		"email": {"other@example.com"},
		"room":  {"other-room"},
	})
	if status != http.StatusOK {
		t.Fatalf("status=%d", status)
	}
	if len(f.mailer.messages) != 0 {
		t.Errorf("messages=%d, want 0 because path room is workshop", len(f.mailer.messages))
	}
}

// AC12.2 — magic-link email content is room-scoped and does not expose a final
// moderator JWT.
func TestModeratorAuth_EmailContainsMagicLinkNotJWT_RT12_6_RT12_9(t *testing.T) {
	f := newModeratorFixture(t, nil)
	f.postForm(t, "/allowed-room/moderator", url.Values{"email": {"mod@example.com"}})
	f.postForm(t, "/workshop/moderator", url.Values{"email": {"mod@example.com"}})
	if len(f.mailer.messages) != 2 {
		t.Fatalf("messages=%d, want 2", len(f.mailer.messages))
	}
	for _, msg := range f.mailer.messages {
		if strings.Contains(msg.link, "jwt=") {
			t.Errorf("magic link contains final jwt: %q", msg.link)
		}
	}
	if f.mailer.messages[0].link == f.mailer.messages[1].link {
		t.Error("different room authorisations produced identical links")
	}
}

// AC12.3 — a valid magic link redirects to a room URL with a room-bound JWT.
func TestModeratorAuth_VerifyRedirectsWithRoomBoundJWT_RT12_10_RT12_11_RT12_12(t *testing.T) {
	f := newModeratorFixture(t, nil)
	f.postForm(t, "/allowed-room/moderator", url.Values{"email": {"mod@example.com"}})
	verifyURL, err := url.Parse(f.mailer.messages[0].link)
	if err != nil {
		t.Fatalf("parse magic link: %v", err)
	}
	status, _, header := f.get(t, verifyURL.RequestURI())
	if status != http.StatusSeeOther {
		t.Fatalf("verify status=%d, want 303", status)
	}
	location := header.Get("Location")
	if !strings.HasPrefix(location, "/allowed-room?jwt=") {
		t.Fatalf("Location=%q", location)
	}
	jwtStr := extractModeratorToken(t, location)
	claims := jwt.MapClaims{}
	parsed, err := jwt.ParseWithClaims(jwtStr, claims, func(tok *jwt.Token) (interface{}, error) {
		return &f.key.PublicKey, nil
	})
	if err != nil || !parsed.Valid {
		t.Fatalf("parse room jwt: valid=%v err=%v", parsed != nil && parsed.Valid, err)
	}
	if claims["room"] != "allowed-room" {
		t.Errorf("jwt room=%v, want allowed-room", claims["room"])
	}
	status, body, _ := f.get(t, location)
	if status != http.StatusOK || !bodyIsMeetingPage(body) {
		t.Fatalf("authorised room status=%d meeting=%v", status, bodyIsMeetingPage(body))
	}
	status, _, _ = f.get(t, "/other-room?jwt="+jwtStr)
	if status == http.StatusOK {
		t.Errorf("room-bound jwt allowed a different room")
	}
}

// AC12.3 — expired, tampered, and reused magic links have no moderator access.
func TestModeratorAuth_InvalidMagicLinksRejected_RT12_13_RT12_14_RT12_15(t *testing.T) {
	t.Run("expired", func(t *testing.T) {
		f := newModeratorFixture(t, func(cfg *server.Config, _ *captureModeratorMailer) {
			cfg.ModeratorAuth.MagicLinkTTL = time.Nanosecond
		})
		f.postForm(t, "/allowed-room/moderator", url.Values{"email": {"mod@example.com"}})
		f.now = f.now.Add(time.Second)
		verifyURL, _ := url.Parse(f.mailer.messages[0].link)
		status, _, _ := f.get(t, verifyURL.RequestURI())
		if status != http.StatusUnauthorized {
			t.Errorf("expired status=%d, want 401", status)
		}
	})
	t.Run("tampered", func(t *testing.T) {
		f := newModeratorFixture(t, nil)
		f.postForm(t, "/allowed-room/moderator", url.Values{"email": {"mod@example.com"}})
		verifyURL, _ := url.Parse(f.mailer.messages[0].link)
		q := verifyURL.Query()
		q.Set("token", q.Get("token")+"x")
		verifyURL.RawQuery = q.Encode()
		status, _, _ := f.get(t, verifyURL.RequestURI())
		if status != http.StatusUnauthorized {
			t.Errorf("tampered status=%d, want 401", status)
		}
	})
	t.Run("reused", func(t *testing.T) {
		f := newModeratorFixture(t, nil)
		f.postForm(t, "/allowed-room/moderator", url.Values{"email": {"mod@example.com"}})
		verifyURL, _ := url.Parse(f.mailer.messages[0].link)
		status, _, _ := f.get(t, verifyURL.RequestURI())
		if status != http.StatusSeeOther {
			t.Fatalf("first use status=%d", status)
		}
		status, _, _ = f.get(t, verifyURL.RequestURI())
		if status != http.StatusUnauthorized {
			t.Errorf("reused status=%d, want 401", status)
		}
	})
}

// AC12.5 — missing or failed SMTP delivery fails closed.
func TestModeratorAuth_DeliveryFailureFailsClosed_RT12_20_RT12_21_RT12_22_RT12_23(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		f := newModeratorFixture(t, func(cfg *server.Config, _ *captureModeratorMailer) {
			cfg.ModeratorAuth.Mailer = nil
		})
		status, body := f.postForm(t, "/allowed-room/moderator", url.Values{"email": {"mod@example.com"}})
		if status != http.StatusOK || !strings.Contains(body, "check your email") {
			t.Fatalf("status=%d body=%q", status, body)
		}
		if rows, err := f.links.All(); err != nil || len(rows) != 0 {
			t.Fatalf("rows=%d err=%v, want no usable token state", len(rows), err)
		}
	})
	t.Run("failed", func(t *testing.T) {
		f := newModeratorFixture(t, func(_ *server.Config, mailer *captureModeratorMailer) {
			mailer.fail = true
		})
		status, body := f.postForm(t, "/allowed-room/moderator", url.Values{"email": {"mod@example.com"}})
		if status != http.StatusOK || !strings.Contains(body, "check your email") {
			t.Fatalf("status=%d body=%q", status, body)
		}
		if strings.Contains(f.logs.String(), "mod@example.com") || strings.Contains(f.logs.String(), "token=") {
			t.Errorf("logs leak email or token material: %s", f.logs.String())
		}
	})
}
