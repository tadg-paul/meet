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
		w.Write([]byte("<!doctype html><title>Invalid link</title><p>Invalid or expired moderator link.</p>"))
		return
	}
	http.Redirect(w, r, moderatorURL, http.StatusSeeOther)
}

func (s *Server) renderModeratorForm(w http.ResponseWriter, room string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	tmpl := template.Must(template.New("moderator").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>Moderator access</title></head>
<body><main><h1>Moderator access</h1><form method="post" action="/{{.Room}}/moderator">
<label for="email">Email address</label>
<input id="email" name="email" type="email" required autofocus>
<button type="submit">Send link</button>
</form></main></body></html>`))
	if err := tmpl.Execute(w, struct{ Room string }{Room: room}); err != nil {
		s.logger.Error("moderator form render failed", "error", err)
	}
}

func (s *Server) renderModeratorCheckEmail(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte("<!doctype html><title>Check your email</title><p>If that address is authorised, check your email for a moderator link.</p>"))
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
