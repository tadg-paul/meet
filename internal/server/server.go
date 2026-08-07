// ABOUTME: HTTP server for meet. Serves the branded 8x8 JaaS page with
// ABOUTME: room name derived from the URL path, plus static assets.

package server

import (
	"bytes"
	"context"
	"crypto/rsa"
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// inactiveRoomMessage is the single explanation shown for every blocked public
// meeting-room slug, regardless of why it is blocked (#13). Keeping the copy
// (and the whole response) identical across states prevents the gate from
// becoming a room-existence oracle.
const inactiveRoomMessage = "This meeting room is not active. If you have been given this link for a meeting, it may be the case that this is the correct meeting room but the room is not active currently. Please check the meeting date and time."

//go:embed all:static
var staticFS embed.FS

//go:embed static/index.html
var indexHTML string

// Config holds the server configuration.
type Config struct {
	Addr    string
	BaseURL string
	AppID   string
	DataDir string
	WebDAV  *WebDAVConfig
	// Stream is the Cloudflare Stream client used for video uploads. When
	// nil, RECORDING_UPLOADED webhooks log "stream not configured" and the
	// file remains in download/ for manual recovery (issue #6).
	Stream             *StreamClient
	Recordings         *RecordingsLog
	PlayerBaseURL      string
	LocalRetentionDays int
	WebhookToken       string
	Logger             *slog.Logger
	// Rooms is the registry consulted by handleRoom. When non-nil, every
	// request to /<room> is gated against this registry (#7). When nil, the
	// gate is disabled and any room name loads the meeting page (legacy).
	Rooms *RoomsLog
	// JWTPublicKey verifies the optional ?jwt= query parameter for the
	// moderator bypass. Nil disables JWT bypass entirely.
	JWTPublicKey *rsa.PublicKey
	// JWTPrivateKey signs room-scoped moderator JWTs after magic-link
	// verification. Nil disables moderator magic-link verification.
	JWTPrivateKey *rsa.PrivateKey
	JWTKeyID      string
	// ModeratorAuth controls the room-scoped moderator magic-link flow.
	ModeratorAuth ModeratorAuthConfig
	// Now returns the current time. Defaults to time.Now in New. Tests
	// inject a controllable clock here.
	Now func() time.Time
}

// Server wraps net/http.Server with meet-specific routing.
type Server struct {
	http   *http.Server
	cfg    Config
	tmpl   *template.Template
	logger *slog.Logger
	dedup  *deduplicator
	now    func() time.Time
	// inactiveBody is the pre-rendered inactive-room page (#13). It is fixed
	// at construction so every blocked slug emits byte-identical output.
	inactiveBody []byte
	// timers is the shared per-room meeting timer (#15).
	timers *TimerHub
}

type pageData struct {
	AppID       string
	RoomName    string
	DomainFull  string
	DomainFirst string
	DomainRest  string
}

// New creates a configured Server ready to listen.
func New(cfg Config) *Server {
	tmpl := template.Must(template.New("index").Parse(indexHTML))

	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	s := &Server{
		cfg:          cfg,
		tmpl:         tmpl,
		logger:       cfg.Logger,
		dedup:        newDeduplicator(1000),
		now:          now,
		inactiveBody: renderInactiveBody(cfg.BaseURL, cfg.Logger),
	}

	// Timer settings persist to the state directory when one is configured;
	// without it the timer runs with default config and rejects set (#15).
	var timerSettings *TimerSettingsLog
	if cfg.DataDir != "" {
		ts, err := NewTimerSettingsLog(cfg.DataDir)
		if err != nil {
			cfg.Logger.Error("failed to open timer settings log", "error", err)
		} else {
			timerSettings = ts
		}
	}
	s.timers = NewTimerHub(timerSettings, now, cfg.Logger)

	mux := http.NewServeMux()

	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		cfg.Logger.Error("failed to create static sub-filesystem", "error", err)
	}
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(sub))))
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/webhook/recording", s.handleWebhook)
	mux.HandleFunc("/", s.handleRoom)

	s.http = &http.Server{
		Addr:    cfg.Addr,
		Handler: mux,
	}

	return s
}

// Handler returns the server's HTTP handler for use in tests.
func (s *Server) Handler() http.Handler {
	return s.http.Handler
}

// ListenAndServe starts the HTTP server.
func (s *Server) ListenAndServe() error {
	return s.http.ListenAndServe()
}

// Shutdown gracefully stops the server.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.http.Shutdown(ctx)
}

// StartPurgeTicker runs a daily ticker that removes files older than the
// configured local-retention window from the uploaded directory. Cancel the
// context to stop.
func (s *Server) StartPurgeTicker(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()

		retention := s.localRetention()
		s.PurgeOldUploads(retention)

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.PurgeOldUploads(s.localRetention())
			}
		}
	}()
}

// localRetention returns the configured local-retention window with a 14-day
// fallback when the field is zero (legacy / test setups).
func (s *Server) localRetention() time.Duration {
	days := s.cfg.LocalRetentionDays
	if days <= 0 {
		days = 14
	}
	return time.Duration(days) * 24 * time.Hour
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}

func (s *Server) handleRoom(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/")
	path = strings.TrimSuffix(path, "/")

	if room, ok := moderatorRoute(path, "moderator"); ok {
		s.handleModerator(w, r, room)
		return
	}
	if room, ok := moderatorRoute(path, "moderator/verify"); ok {
		s.handleModeratorVerify(w, r, room)
		return
	}
	if room, ok := timerRoute(path, "timer/events"); ok {
		s.handleTimerEvents(w, r, room)
		return
	}
	if room, ok := timerRoute(path, "timer"); ok {
		s.handleTimer(w, r, room)
		return
	}

	// Reject paths with slashes (only single-segment room names).
	if strings.Contains(path, "/") {
		http.Error(w, "Invalid room name", http.StatusBadRequest)
		return
	}

	// Registry gate (#7). A non-empty path must either be a registered room
	// inside its valid window, or be accompanied by a valid moderator JWT.
	// The empty path (and any other blocked slug) falls through to the single
	// inactive-room response (#13), which is identical across every blocked
	// state so it cannot be used as a room-existence oracle.
	if !s.gateAllows(path, r) {
		s.renderInactiveRoom(w)
		return
	}

	domainFull, domainFirst, domainRest := parseDomain(s.cfg.BaseURL)

	data := pageData{
		AppID:       s.cfg.AppID,
		RoomName:    path,
		DomainFull:  domainFull,
		DomainFirst: domainFirst,
		DomainRest:  domainRest,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := s.tmpl.Execute(w, data); err != nil {
		s.logger.Error("template render failed", "error", err)
	}
}

// renderInactiveRoom writes the fixed inactive-room page with HTTP 404. Headers
// and body are constant, so every blocked slug and every blocked state produce
// an identical response (#13, AC13.1). Cache-Control: no-store keeps an expired
// window from being replayed from cache (AC13.4).
func (s *Server) renderInactiveRoom(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("Content-Length", strconv.Itoa(len(s.inactiveBody)))
	w.WriteHeader(http.StatusNotFound)
	w.Write(s.inactiveBody)
}

// renderInactiveBody pre-renders the inactive-room page once at construction.
// It reuses the moderator-auth page template for banner/domain styling (#13),
// with no form and the fixed inactive copy.
func renderInactiveBody(baseURL string, logger *slog.Logger) []byte {
	_, domainFirst, domainRest := parseDomain(baseURL)
	tmpl := template.Must(template.New("inactive").Parse(moderatorAuthPageHTML))
	var buf bytes.Buffer
	err := tmpl.Execute(&buf, moderatorAuthPageData{
		Title:       "Inactive",
		Heading:     "Inactive",
		Message:     inactiveRoomMessage,
		DomainFirst: domainFirst,
		DomainRest:  domainRest,
	})
	if err != nil && logger != nil {
		logger.Error("inactive page render failed", "error", err)
	}
	return buf.Bytes()
}

// parseDomain extracts the domain from a URL and splits it into the first
// label (bright) and the rest (dim). E.g. "https://meet.example.com" yields
// ("meet.example.com", "meet", ".example.com").
func parseDomain(baseURL string) (full, first, rest string) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return baseURL, baseURL, ""
	}

	host := u.Hostname()
	full = host

	dot := strings.Index(host, ".")
	if dot < 0 {
		return host, host, ""
	}

	first = host[:dot]
	rest = host[dot:]
	return full, first, rest
}

// gateAllows reports whether the given room path should load the meeting page.
//
// Rules:
//   - A valid moderator JWT in the request bypasses every check.
//   - Without a JWT, the room must exist in the registry, be in the "created"
//     state, and the current time must fall within [valid_from, valid_until]
//     inclusive.
//   - If the server was constructed without a RoomsLog (Config.Rooms == nil),
//     the gate is disabled and every non-empty path is allowed; the empty
//     path is rejected. This preserves the original "no gating" path for any
//     caller that has not opted in.
func (s *Server) gateAllows(path string, r *http.Request) bool {
	if s.hasValidModeratorJWT(path, r) {
		return true
	}
	if path == "" {
		return false
	}
	if s.cfg.Rooms == nil {
		// Registry not wired (legacy / test setups). Any non-empty room loads.
		return true
	}
	entry := s.cfg.Rooms.LatestByRoom(path)
	if entry == nil || entry.Status != RoomCreated {
		return false
	}
	now := s.now()
	if now.Before(entry.ValidFrom) || now.After(entry.ValidUntil) {
		return false
	}
	return true
}

// hasValidModeratorJWT returns true when the request carries a ?jwt= query
// parameter whose token verifies against the configured public key and
// asserts moderator=true. Any failure path returns false (treated as no JWT).
func (s *Server) hasValidModeratorJWT(path string, r *http.Request) bool {
	tokenStr := r.URL.Query().Get("jwt")
	if tokenStr == "" {
		return false
	}
	if s.cfg.JWTPublicKey == nil {
		return false
	}
	token, err := jwt.Parse(tokenStr, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return s.cfg.JWTPublicKey, nil
	})
	if err != nil || token == nil || !token.Valid {
		return false
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return false
	}
	claimRoom, ok := claims["room"].(string)
	if !ok || (claimRoom != "*" && claimRoom != path) {
		return false
	}
	ctx, ok := claims["context"].(map[string]any)
	if !ok {
		return false
	}
	user, ok := ctx["user"].(map[string]any)
	if !ok {
		return false
	}
	// 8x8 JWTs use the string "true" for moderator (per docs/8x8-embed.md).
	switch v := user["moderator"].(type) {
	case string:
		return v == "true"
	case bool:
		return v
	}
	return false
}

func moderatorRoute(path, suffix string) (string, bool) {
	want := "/" + suffix
	if !strings.HasSuffix(path, want) {
		return "", false
	}
	room := strings.TrimSuffix(path, want)
	if room == "" || strings.Contains(room, "/") {
		return "", false
	}
	return room, true
}
