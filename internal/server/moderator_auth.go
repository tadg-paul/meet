// ABOUTME: Room-scoped moderator magic-link authentication.
// ABOUTME: Authorizes email/room pairs, issues one-use links, and mints room-bound JWTs.

package server

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"net/mail"
	"net/smtp"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const moderatorLinksLogFile = "moderator_links.csv"

type ModeratorMailer interface {
	SendModeratorMagicLink(to, link string) error
}

type ModeratorAuthConfig struct {
	Enabled         bool
	MagicLinkTTL    time.Duration
	ModeratorJWTTTL time.Duration
	SigningKey      []byte
	FromEmail       string
	SMTP            SMTPConfig
	Rooms           map[string][]string
	Links           *ModeratorLinksLog
	Mailer          ModeratorMailer
}

type SMTPConfig struct {
	Host string
	Port int
	User string
	Pass string
}

type ModeratorLinksLog struct {
	mu       sync.Mutex
	filePath string
}

type ModeratorLinkEntry struct {
	Timestamp time.Time
	TokenHash string
	EmailHash string
	Room      string
	Status    string
	ExpiresAt time.Time
	Note      string
}

type moderatorTokenPayload struct {
	Email  string `json:"email"`
	Room   string `json:"room"`
	Nonce  string `json:"nonce"`
	Expiry int64  `json:"exp"`
}

type moderatorAuthPageData struct {
	Title       string
	Heading     string
	Message     string
	Room        string
	ShowForm    bool
	DomainFirst string
	DomainRest  string
}

type SMTPModeratorMailer struct {
	From string
	SMTP SMTPConfig
}

func NewModeratorLinksLog(stateDir string) (*ModeratorLinksLog, error) {
	filePath := filepath.Join(stateDir, moderatorLinksLogFile)
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		if err := os.MkdirAll(stateDir, 0o700); err != nil {
			return nil, fmt.Errorf("creating state dir: %w", err)
		}
		f, err := os.Create(filePath)
		if err != nil {
			return nil, fmt.Errorf("creating moderator links log: %w", err)
		}
		w := csv.NewWriter(f)
		if err := w.Write([]string{"timestamp", "token_hash", "email_hash", "room", "status", "expires_at", "note"}); err != nil {
			f.Close()
			return nil, fmt.Errorf("writing moderator links header: %w", err)
		}
		w.Flush()
		if err := f.Close(); err != nil {
			return nil, fmt.Errorf("closing moderator links log: %w", err)
		}
	} else if err != nil {
		return nil, fmt.Errorf("statting moderator links log: %w", err)
	}
	return &ModeratorLinksLog{filePath: filePath}, nil
}

func (l *ModeratorLinksLog) Append(entry ModeratorLinkEntry) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	f, err := os.OpenFile(l.filePath, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("opening moderator links log: %w", err)
	}
	defer f.Close()

	w := csv.NewWriter(f)
	if err := w.Write([]string{
		entry.Timestamp.UTC().Format(time.RFC3339),
		entry.TokenHash,
		entry.EmailHash,
		entry.Room,
		entry.Status,
		entry.ExpiresAt.UTC().Format(time.RFC3339),
		entry.Note,
	}); err != nil {
		return fmt.Errorf("writing moderator link row: %w", err)
	}
	w.Flush()
	return w.Error()
}

func (l *ModeratorLinksLog) All() ([]ModeratorLinkEntry, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	f, err := os.Open(l.filePath)
	if err != nil {
		return nil, fmt.Errorf("opening moderator links log: %w", err)
	}
	defer f.Close()

	r := csv.NewReader(f)
	r.FieldsPerRecord = -1
	rows, err := r.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("reading moderator links log: %w", err)
	}
	out := make([]ModeratorLinkEntry, 0, len(rows))
	for i, row := range rows {
		if i == 0 || len(row) < 6 {
			continue
		}
		ts, err := time.Parse(time.RFC3339, row[0])
		if err != nil {
			continue
		}
		expires, err := time.Parse(time.RFC3339, row[5])
		if err != nil {
			continue
		}
		entry := ModeratorLinkEntry{
			Timestamp: ts,
			TokenHash: row[1],
			EmailHash: row[2],
			Room:      row[3],
			Status:    row[4],
			ExpiresAt: expires,
		}
		if len(row) >= 7 {
			entry.Note = row[6]
		}
		out = append(out, entry)
	}
	return out, nil
}

func (l *ModeratorLinksLog) LatestByTokenHash(tokenHash string) (*ModeratorLinkEntry, error) {
	rows, err := l.All()
	if err != nil {
		return nil, err
	}
	for i := len(rows) - 1; i >= 0; i-- {
		if rows[i].TokenHash == tokenHash {
			entry := rows[i]
			return &entry, nil
		}
	}
	return nil, nil
}

func (m SMTPModeratorMailer) SendModeratorMagicLink(to, link string) error {
	if m.SMTP.Host == "" {
		return fmt.Errorf("smtp host not configured")
	}
	port := m.SMTP.Port
	if port == 0 {
		port = 587
	}
	from := mail.Address{Address: m.From}
	msg := strings.Join([]string{
		"From: " + from.String(),
		"To: " + (&mail.Address{Address: to}).String(),
		"Subject: Your moderator link",
		"",
		"Open this moderator link:",
		"",
		link,
		"",
		"This link expires shortly.",
	}, "\r\n")
	auth := smtp.PlainAuth("", m.SMTP.User, m.SMTP.Pass, m.SMTP.Host)
	return smtp.SendMail(fmt.Sprintf("%s:%d", m.SMTP.Host, port), auth, m.From, []string{to}, []byte(msg))
}

func (s *Server) handleModerator(w http.ResponseWriter, r *http.Request, room string) {
	if !validRoomSegment(room) {
		http.NotFound(w, r)
		return
	}
	// #14: the moderator entry route is available only when the room is active
	// now. For any not-active room it returns the same inactive-room 404 the
	// guest gate serves, so it cannot be probed as a slug oracle.
	if !s.roomActiveNow(room) {
		s.renderInactiveRoom(w)
		return
	}
	switch r.Method {
	case http.MethodGet:
		s.renderModeratorForm(w, room)
	case http.MethodPost:
		s.handleModeratorSubmit(w, r, room)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleModeratorVerify(w http.ResponseWriter, r *http.Request, room string) {
	if !validRoomSegment(room) {
		http.NotFound(w, r)
		return
	}
	token := r.URL.Query().Get("token")
	moderatorURL, err := s.verifyModeratorMagicLink(room, token)
	if err != nil {
		s.logger.Warn("moderator magic link verification failed", "room", room, "error", err)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusUnauthorized)
		s.renderModeratorAuthPage(w, moderatorAuthPageData{
			Title:   "Invalid link",
			Heading: "Invalid link",
			Message: "Invalid or expired login link.",
		})
		return
	}
	http.Redirect(w, r, moderatorURL, http.StatusSeeOther)
}

func (s *Server) renderModeratorForm(w http.ResponseWriter, room string) {
	s.renderModeratorAuthPage(w, moderatorAuthPageData{
		Title:    "Login",
		Heading:  "Login",
		Message:  "Enter the preapproved email address for this room.",
		Room:     room,
		ShowForm: true,
	})
}

func (s *Server) renderModeratorCheckEmail(w http.ResponseWriter) {
	s.renderModeratorAuthPage(w, moderatorAuthPageData{
		Title:   "Check your email",
		Heading: "Check your email",
		Message: "If that address is authorised, check your email for a login link.",
	})
}

func (s *Server) renderModeratorAuthPage(w http.ResponseWriter, data moderatorAuthPageData) {
	_, data.DomainFirst, data.DomainRest = parseDomain(s.cfg.BaseURL)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	tmpl := template.Must(template.New("moderator-auth").Parse(moderatorAuthPageHTML))
	if err := tmpl.Execute(w, data); err != nil {
		s.logger.Error("moderator auth page render failed", "error", err)
	}
}

func (s *Server) handleModeratorSubmit(w http.ResponseWriter, r *http.Request, room string) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	email := strings.TrimSpace(r.FormValue("email"))
	if _, err := mail.ParseAddress(email); err != nil {
		http.Error(w, "invalid email address", http.StatusBadRequest)
		return
	}
	if !s.moderatorAuthorised(room, email) {
		s.renderModeratorCheckEmail(w)
		return
	}
	if _, err := s.createAndDeliverModeratorMagicLink(room, email, "delivered"); err != nil {
		s.logger.Warn("moderator magic link delivery failed", "room", room, "error", err)
	}
	s.renderModeratorCheckEmail(w)
}

func (s *Server) createAndDeliverModeratorMagicLink(room, email, status string) (string, error) {
	cfg := s.cfg.ModeratorAuth
	if cfg.Links == nil {
		return "", fmt.Errorf("moderator links log not configured")
	}
	link, tokenHash, expiresAt, err := s.createModeratorMagicLink(room, email)
	if err != nil {
		return "", err
	}
	if status == "delivered" {
		if cfg.Mailer == nil {
			return "", fmt.Errorf("moderator mailer not configured")
		}
		if err := cfg.Mailer.SendModeratorMagicLink(email, link); err != nil {
			return "", err
		}
	}
	if err := cfg.Links.Append(ModeratorLinkEntry{
		Timestamp: s.now(),
		TokenHash: tokenHash,
		EmailHash: hashString(strings.ToLower(email)),
		Room:      room,
		Status:    status,
		ExpiresAt: expiresAt,
	}); err != nil {
		return "", err
	}
	return link, nil
}

func (s *Server) CreatePrintedModeratorMagicLink(room, email string) (string, error) {
	if !validRoomSegment(room) {
		return "", fmt.Errorf("invalid room")
	}
	if _, err := mail.ParseAddress(email); err != nil {
		return "", fmt.Errorf("invalid email address")
	}
	if !s.moderatorAuthorised(room, email) {
		return "", fmt.Errorf("email-room pair not authorised")
	}
	return s.createAndDeliverModeratorMagicLink(room, email, "printed")
}

func (s *Server) CreateDeliveredModeratorMagicLink(room, email string) (string, error) {
	if !validRoomSegment(room) {
		return "", fmt.Errorf("invalid room")
	}
	if _, err := mail.ParseAddress(email); err != nil {
		return "", fmt.Errorf("invalid email address")
	}
	if !s.moderatorAuthorised(room, email) {
		return "", fmt.Errorf("email-room pair not authorised")
	}
	return s.createAndDeliverModeratorMagicLink(room, email, "delivered")
}

func (s *Server) VerifyModeratorMagicLinkForCLI(room, token string) (string, error) {
	return s.verifyModeratorMagicLink(room, token)
}

func (s *Server) createModeratorMagicLink(room, email string) (link, tokenHash string, expiresAt time.Time, err error) {
	cfg := s.cfg.ModeratorAuth
	if len(cfg.SigningKey) == 0 {
		return "", "", time.Time{}, fmt.Errorf("moderator signing key not configured")
	}
	ttl := cfg.MagicLinkTTL
	if ttl <= 0 {
		ttl = 15 * time.Minute
	}
	expiresAt = s.now().Add(ttl)
	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		return "", "", time.Time{}, fmt.Errorf("generating nonce: %w", err)
	}
	payload := moderatorTokenPayload{
		Email:  email,
		Room:   room,
		Nonce:  base64.RawURLEncoding.EncodeToString(nonce),
		Expiry: expiresAt.Unix(),
	}
	token, err := signModeratorPayload(payload, cfg.SigningKey)
	if err != nil {
		return "", "", time.Time{}, err
	}
	base := strings.TrimRight(s.cfg.BaseURL, "/")
	if base == "" {
		base = "https://" + strings.TrimSpace(s.cfg.Addr)
	}
	return fmt.Sprintf("%s/%s/moderator/verify?token=%s", base, room, url.QueryEscape(token)), hashString(token), expiresAt, nil
}

func signModeratorPayload(payload moderatorTokenPayload, key []byte) (string, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshalling moderator token: %w", err)
	}
	payloadB64 := base64.RawURLEncoding.EncodeToString(raw)
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(payloadB64))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return payloadB64 + "." + sig, nil
}

func verifyModeratorPayload(token string, key []byte) (moderatorTokenPayload, error) {
	var payload moderatorTokenPayload
	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 {
		return payload, fmt.Errorf("malformed token")
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(parts[0]))
	expected := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(parts[1]), []byte(expected)) {
		return payload, fmt.Errorf("invalid signature")
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return payload, fmt.Errorf("decoding token: %w", err)
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return payload, fmt.Errorf("parsing token: %w", err)
	}
	return payload, nil
}

func (s *Server) verifyModeratorMagicLink(room, token string) (string, error) {
	cfg := s.cfg.ModeratorAuth
	if token == "" {
		return "", fmt.Errorf("missing token")
	}
	if cfg.Links == nil {
		return "", fmt.Errorf("moderator links log not configured")
	}
	payload, err := verifyModeratorPayload(token, cfg.SigningKey)
	if err != nil {
		return "", err
	}
	if payload.Room != room {
		return "", fmt.Errorf("wrong room")
	}
	if s.now().Unix() > payload.Expiry {
		return "", fmt.Errorf("token expired")
	}
	if !s.moderatorAuthorised(room, payload.Email) {
		return "", fmt.Errorf("moderator no longer authorised")
	}
	tokenHash := hashString(token)
	latest, err := cfg.Links.LatestByTokenHash(tokenHash)
	if err != nil {
		return "", err
	}
	if latest == nil || (latest.Status != "delivered" && latest.Status != "printed") {
		return "", fmt.Errorf("token not active")
	}
	if err := cfg.Links.Append(ModeratorLinkEntry{
		Timestamp: s.now(),
		TokenHash: tokenHash,
		EmailHash: hashString(strings.ToLower(payload.Email)),
		Room:      room,
		Status:    "used",
		ExpiresAt: time.Unix(payload.Expiry, 0),
	}); err != nil {
		return "", err
	}
	jwtStr, err := s.signModeratorJWT(room)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("/%s?jwt=%s", room, url.QueryEscape(jwtStr)), nil
}

func (s *Server) moderatorAuthorised(room, email string) bool {
	if !s.cfg.ModeratorAuth.Enabled {
		return false
	}
	allowed := s.cfg.ModeratorAuth.Rooms[room]
	email = strings.ToLower(strings.TrimSpace(email))
	for _, candidate := range allowed {
		if strings.ToLower(strings.TrimSpace(candidate)) == email {
			return true
		}
	}
	return false
}

func (s *Server) signModeratorJWT(room string) (string, error) {
	if s.cfg.JWTPrivateKey == nil {
		return "", fmt.Errorf("jwt private key not configured")
	}
	ttl := s.cfg.ModeratorAuth.ModeratorJWTTTL
	if ttl <= 0 {
		ttl = 2 * time.Hour
	}
	now := time.Now()
	claims := ModeratorJWTClaims(s.cfg.AppID, room, "Moderator", now, ttl)
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = s.cfg.JWTKeyID
	return tok.SignedString(s.cfg.JWTPrivateKey)
}

func ModeratorJWTClaims(appID, room, name string, now time.Time, ttl time.Duration) jwt.MapClaims {
	return jwt.MapClaims{
		"aud":  "jitsi",
		"iss":  "chat",
		"sub":  appID,
		"room": room,
		"iat":  now.Unix(),
		"nbf":  now.Unix(),
		"exp":  now.Add(ttl).Unix(),
		"context": map[string]interface{}{
			"user": map[string]interface{}{
				"name":      name,
				"moderator": "true",
			},
			"features": map[string]interface{}{
				"recording": true,
			},
		},
	}
}

func validRoomSegment(room string) bool {
	return room != "" && !strings.Contains(room, "/")
}

func hashString(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

const moderatorAuthPageHTML = `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <title>{{.Title}}</title>
    <style>
        @font-face {
            font-family: 'Special Elite';
            src: url('/static/SpecialElite-Regular.woff2') format('woff2');
            font-display: swap;
        }

        *, *::before, *::after {
            box-sizing: border-box;
            margin: 0;
            padding: 0;
        }

        html {
            font-size: 100%;
        }

        body.auth-page {
            min-height: 100vh;
            padding-top: 3.5rem;
            background: #f5f5f5;
            color: #333;
            font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
        }

        .banner {
            position: fixed;
            top: 0;
            right: 0;
            left: 0;
            z-index: 100;
            display: flex;
            align-items: center;
            justify-content: center;
            flex-shrink: 0;
            padding: 0.5rem 1rem;
            background: #1a3a5c;
        }

        .banner a {
            font-family: 'Special Elite', serif;
            font-size: 2rem;
            line-height: 1;
            text-decoration: none;
        }

        .banner .domain-highlight {
            color: #f0f0f0;
        }

        .banner .domain-dim {
            color: #8a9bae;
        }

        .auth-card {
            max-width: 28rem;
            margin: 3rem auto;
            padding: 2rem;
            border-radius: 0.5rem;
            background: #fff;
            box-shadow: 0 0.125rem 0.5rem rgba(0, 0, 0, 0.08);
        }

        .auth-card h1 {
            margin-bottom: 1rem;
            font-size: 1.25rem;
        }

        .auth-card p {
            margin-bottom: 1rem;
            color: #555;
            line-height: 1.5;
        }

        .auth-card label {
            display: block;
            margin-bottom: 0.375rem;
            font-size: 0.875rem;
            font-weight: 500;
        }

        .auth-card input[type="email"] {
            width: 100%;
            margin-bottom: 1rem;
            padding: 0.625rem;
            border: 0.0625rem solid #ccc;
            border-radius: 0.25rem;
            font-size: 1rem;
        }

        .auth-card input[type="email"]:focus {
            border-color: #2563eb;
            outline: none;
            box-shadow: 0 0 0 0.125rem rgba(37, 99, 235, 0.2);
        }

        .auth-card button[type="submit"] {
            width: 100%;
            padding: 0.625rem;
            border: 0;
            border-radius: 0.25rem;
            background: #2563eb;
            color: #fff;
            font-size: 1rem;
            cursor: pointer;
        }

        .auth-card button[type="submit"]:hover {
            background: #1d4ed8;
        }
    </style>
</head>
<body class="auth-page">
    <div class="banner">
        <a href="/">
            <span class="domain-highlight">{{.DomainFirst}}</span><span class="domain-dim">{{.DomainRest}}</span>
        </a>
    </div>
    <main class="auth-card">
        <h1>{{.Heading}}</h1>
        <p>{{.Message}}</p>
        {{if .ShowForm}}
        <form method="post" action="/{{.Room}}/moderator">
            <label for="email">Email address</label>
            <input id="email" name="email" type="email" required placeholder="you@example.com" autofocus>
            <button type="submit">Send link</button>
        </form>
        {{end}}
    </main>
</body>
</html>`
