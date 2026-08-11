// ABOUTME: meet entrypoint. Subcommands serve the app and manage rooms.
// ABOUTME: Help prose is sourced from docs/help and staged at build time.

package main

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	_ "embed"
	"encoding/pem"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/tigger-developer/meet/internal/server"
	"gopkg.in/yaml.v3"
)

// Help text is copied from docs/help/meet*.txt into this package directory
// by the Makefile stage-help-text target before build. The canonical
// sources live in docs/help/; the staged copies here are gitignored. Do
// not edit the *.txt files in this directory — edit docs/help/ and rebuild.

//go:embed help-meet.txt
var helpMeet string

//go:embed help-serve.txt
var helpServe string

//go:embed help-token.txt
var helpToken string

//go:embed help-create.txt
var helpCreate string

//go:embed help-cancel.txt
var helpCancel string

//go:embed help-list.txt
var helpList string

// Version is the build identifier, overridden at link time.
var Version = "dev"

type appConfig struct {
	Addr                 string     `yaml:"addr"`
	BaseURL              string     `yaml:"base_url"`
	DefaultModeratorName string     `yaml:"default-moderator-name"`
	Keys8x8              keys8x8    `yaml:"8x8-keys"`
	Recording            recording  `yaml:"recording"`
	ModeratorAuth        modAuth    `yaml:"moderator-auth"`
	Meeting              meetingCfg `yaml:"meeting"`
}

// meetingCfg holds create-time defaults for scheduled meetings (#17). Values
// use the compound duration format (e.g. "4h", "4:30h", "15m").
type meetingCfg struct {
	DefaultDuration  string `yaml:"default-duration"`
	DefaultOpenEarly string `yaml:"default-open-early"`
}

type recording struct {
	WebDAV             webdav     `yaml:"webdav"`
	WebhookToken       string     `yaml:"webhook-token"`
	PlayerBaseURL      string     `yaml:"player-base-url"`
	LocalRetentionDays int        `yaml:"local-retention-days"`
	Cloudflare         cloudflare `yaml:"cloudflare"`
	SMTP               smtp       `yaml:"smtp"`
}

type webdav struct {
	URL      string `yaml:"url"`
	User     string `yaml:"user"`
	Password string `yaml:"password"`
	Path     string `yaml:"path"`
}

type cloudflare struct {
	APIToken           string `yaml:"api-token"`
	AccountID          string `yaml:"account-id"`
	StreamCustomerCode string `yaml:"stream-customer-code"`
	StreamTTLDays      int    `yaml:"stream-ttl-days"`
}

type smtp struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
	User string `yaml:"user"`
	Pass string `yaml:"pass"`
}

type modAuth struct {
	Enabled         bool               `yaml:"enabled"`
	MagicLinkTTL    string             `yaml:"magic-link-ttl"`
	ModeratorJWTTTL string             `yaml:"moderator-jwt-ttl"`
	SigningKey      string             `yaml:"signing-key"`
	FromEmail       string             `yaml:"from-email"`
	SMTP            smtp               `yaml:"smtp"`
	Rooms           map[string]modRoom `yaml:"rooms"`
}

type modRoom struct {
	Moderators []string `yaml:"moderators"`
}

type keys8x8 struct {
	AppID      string `yaml:"app-id"`
	KeyID      string `yaml:"key-id"`
	PrivateKey string `yaml:"private-key"`
	PublicKey  string `yaml:"public-key"`
}

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "serve":
			runServe(os.Args[2:])
			return
		case "token":
			runToken(os.Args[2:])
			return
		case "moderator-link":
			runModeratorLink(os.Args[2:])
			return
		case "moderator-verify":
			runModeratorVerify(os.Args[2:])
			return
		case "create":
			runCreate(os.Args[2:])
			return
		case "cancel":
			runCancel(os.Args[2:])
			return
		case "list":
			runList(os.Args[2:])
			return
		case "-h", "--help":
			printUsage()
			os.Exit(0)
		case "--version", "-version":
			fmt.Println(Version)
			os.Exit(0)
		}
	}
	runServe(os.Args[1:])
}

func printUsage() {
	fmt.Fprint(os.Stderr, helpMeet)
}

func runServe(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, helpServe)
	}
	versionFlag := fs.Bool("version", false, "print version and exit")
	configFlag := fs.String("config", "", "comma-separated config files (default: auto from env)")
	fs.Parse(args)

	if *versionFlag {
		fmt.Println(Version)
		os.Exit(0)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))

	configPaths := *configFlag
	if configPaths == "" {
		configPaths = buildConfigPaths()
	}
	cfg := loadConfig(configPaths, logger)

	// ADDR env var takes precedence over addr in config YAML.
	// NixOS hosts allocate a port at deploy time via ADDR.
	if envAddr := os.Getenv("ADDR"); envAddr != "" {
		cfg.Addr = envAddr
	}

	if cfg.Keys8x8.AppID == "" {
		logger.Error("app-id not configured — add it to a secrets YAML file")
		os.Exit(1)
	}

	// DataDir: use STATE_DIRECTORY from systemd, or fall back to current directory.
	dataDir := os.Getenv("STATE_DIRECTORY")
	if dataDir == "" {
		dataDir = "."
	}

	// Open the rooms registry. handleRoom consults this to gate every
	// request; without it, the gate is bypassed and any path serves the
	// meeting page.
	rooms, err := server.NewRoomsLog(dataDir)
	if err != nil {
		logger.Error("opening rooms registry", "error", err)
		os.Exit(1)
	}

	// Parse the 8x8 public key for moderator-JWT verification at the gate.
	// Optional: if absent, JWT bypass is disabled but the registry still works.
	var pubKey *rsa.PublicKey
	if cfg.Keys8x8.PublicKey != "" {
		parsed, err := parsePublicKey(cfg.Keys8x8.PublicKey)
		if err != nil {
			logger.Error("parsing public key — JWT bypass disabled", "error", err)
		} else {
			pubKey = parsed
		}
	}
	var privKey *rsa.PrivateKey
	if cfg.ModeratorAuth.Enabled {
		if cfg.Keys8x8.PrivateKey == "" {
			logger.Error("private-key not configured — moderator auth cannot mint room JWTs")
			os.Exit(1)
		}
		privKey = parsePrivateKey(cfg.Keys8x8.PrivateKey)
	}

	// Open the recordings log. Tracks CF Stream uploads (#6).
	recordings, err := server.NewRecordingsLog(dataDir)
	if err != nil {
		logger.Error("opening recordings log", "error", err)
		os.Exit(1)
	}
	moderatorLinks, err := server.NewModeratorLinksLog(dataDir)
	if err != nil {
		logger.Error("opening moderator links log", "error", err)
		os.Exit(1)
	}

	// Resolve defaults for the non-secret #6 fields.
	playerBaseURL := cfg.Recording.PlayerBaseURL
	if playerBaseURL == "" {
		playerBaseURL = "https://media.lobb.ie/"
	}
	streamTTLDays := cfg.Recording.Cloudflare.StreamTTLDays
	if streamTTLDays <= 0 {
		streamTTLDays = 90
	}
	localRetentionDays := cfg.Recording.LocalRetentionDays
	if localRetentionDays <= 0 {
		localRetentionDays = 14
	}

	// Construct the Stream client if creds are present.
	var streamClient *server.StreamClient
	if cfg.Recording.Cloudflare.AccountID != "" && cfg.Recording.Cloudflare.APIToken != "" {
		streamClient = server.NewStreamClient(server.StreamConfig{
			AccountID: cfg.Recording.Cloudflare.AccountID,
			APIToken:  cfg.Recording.Cloudflare.APIToken,
			TTLDays:   streamTTLDays,
		}, nil)
		logger.Info("Cloudflare Stream upload configured",
			"player_base_url", playerBaseURL,
			"stream_ttl_days", streamTTLDays,
		)
	} else {
		logger.Warn("Cloudflare Stream not configured — recording uploads will fail until cloudflare.account-id and cloudflare.api-token are set")
	}

	srvCfg := server.Config{
		Addr:               cfg.Addr,
		BaseURL:            cfg.BaseURL,
		AppID:              cfg.Keys8x8.AppID,
		DataDir:            dataDir,
		WebhookToken:       cfg.Recording.WebhookToken,
		Logger:             logger,
		Rooms:              rooms,
		JWTPublicKey:       pubKey,
		JWTPrivateKey:      privKey,
		JWTKeyID:           cfg.Keys8x8.KeyID,
		ModeratorAuth:      buildModeratorAuthConfig(cfg, moderatorLinks),
		Stream:             streamClient,
		Recordings:         recordings,
		PlayerBaseURL:      playerBaseURL,
		LocalRetentionDays: localRetentionDays,
	}

	if cfg.Recording.WebDAV.URL != "" {
		srvCfg.WebDAV = &server.WebDAVConfig{
			URL:      cfg.Recording.WebDAV.URL,
			Path:     cfg.Recording.WebDAV.Path,
			User:     cfg.Recording.WebDAV.User,
			Password: cfg.Recording.WebDAV.Password,
		}
		logger.Info("WebDAV recording storage configured", "path", cfg.Recording.WebDAV.Path)
	}

	srv := server.New(srvCfg)

	logger.Info("meet starting", "version", Version, "addr", cfg.Addr, "base_url", cfg.BaseURL)

	// Recover any downloads that weren't uploaded before last shutdown.
	srv.RecoverPendingUploads()

	// Start daily purge of old uploads (>30 days).
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv.StartPurgeTicker(ctx)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	go func() {
		sig := <-sigCh
		logger.Info("shutdown signal received", "signal", sig.String())
		cancel() // stop purge ticker
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer shutdownCancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			logger.Error("shutdown error", "error", err)
		}
	}()

	if err := srv.ListenAndServe(); err != nil && err.Error() != "http: Server closed" {
		logger.Error("server exited", "error", err)
		os.Exit(1)
	}
}

func runToken(args []string) {
	fs := flag.NewFlagSet("token", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, helpToken)
	}
	configFlag := fs.String("config", "", "comma-separated config files (default: auto from env)")
	roomFlag := fs.String("room", "", "room name (required)")
	nameFlag := fs.String("name", "", "display name in the meeting (default from config or \"Moderator\")")
	expiryFlag := fs.Duration("expiry", 2*time.Hour, "token validity duration")
	fs.Parse(args)

	if *roomFlag == "" {
		fs.Usage()
		os.Exit(2)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))

	configPaths := *configFlag
	if configPaths == "" {
		configPaths = buildConfigPaths()
	}
	cfg := loadConfig(configPaths, logger)

	// Resolve display name: CLI flag > config > fallback
	displayName := *nameFlag
	if displayName == "" {
		displayName = cfg.DefaultModeratorName
	}
	if displayName == "" {
		displayName = "Moderator"
	}

	if cfg.Keys8x8.AppID == "" {
		fmt.Fprintln(os.Stderr, "error: app-id not configured")
		os.Exit(1)
	}
	if cfg.Keys8x8.KeyID == "" {
		fmt.Fprintln(os.Stderr, "error: key-id not configured")
		os.Exit(1)
	}
	if cfg.Keys8x8.PrivateKey == "" {
		fmt.Fprintln(os.Stderr, "error: private-key not configured")
		os.Exit(1)
	}

	privKey := parsePrivateKey(cfg.Keys8x8.PrivateKey)

	now := time.Now()
	claims := server.ModeratorJWTClaims(cfg.Keys8x8.AppID, "*", displayName, now, *expiryFlag)

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = cfg.Keys8x8.KeyID

	signed, err := token.SignedString(privKey)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: failed to sign JWT: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("%s/%s?jwt=%s\n", cfg.BaseURL, *roomFlag, signed)
}

func runModeratorLink(args []string) {
	fs := flag.NewFlagSet("moderator-link", flag.ExitOnError)
	configFlag := fs.String("config", "", "comma-separated config files (default: auto from env)")
	roomFlag := fs.String("room", "", "room name (required)")
	emailFlag := fs.String("email", "", "moderator email (required)")
	printFlag := fs.Bool("print", false, "print the magic link")
	sendFlag := fs.Bool("send", false, "send the magic link by SMTP")
	fs.Parse(args)

	if *roomFlag == "" || *emailFlag == "" || (!*printFlag && !*sendFlag) {
		fmt.Fprintln(os.Stderr, "usage: meet moderator-link --room <room> --email <email> (--print | --send)")
		os.Exit(2)
	}
	srv, _ := moderatorCLIServer(*configFlag)
	var (
		link string
		err  error
	)
	if *printFlag {
		link, err = srv.CreatePrintedModeratorMagicLink(*roomFlag, *emailFlag)
	} else {
		link, err = srv.CreateDeliveredModeratorMagicLink(*roomFlag, *emailFlag)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	if *printFlag {
		fmt.Println(link)
	}
}

func runModeratorVerify(args []string) {
	fs := flag.NewFlagSet("moderator-verify", flag.ExitOnError)
	configFlag := fs.String("config", "", "comma-separated config files (default: auto from env)")
	roomFlag := fs.String("room", "", "room name (required)")
	tokenFlag := fs.String("token", "", "magic link token (required)")
	fs.Parse(args)

	if *roomFlag == "" || *tokenFlag == "" {
		fmt.Fprintln(os.Stderr, "usage: meet moderator-verify --room <room> --token <token>")
		os.Exit(2)
	}
	srv, cfg := moderatorCLIServer(*configFlag)
	moderatorURL, err := srv.VerifyModeratorMagicLinkForCLI(*roomFlag, *tokenFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("%s%s\n", strings.TrimRight(cfg.BaseURL, "/"), moderatorURL)
}

func moderatorCLIServer(configFlag string) (*server.Server, appConfig) {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	configPaths := configFlag
	if configPaths == "" {
		configPaths = buildConfigPaths()
	}
	cfg := loadConfig(configPaths, logger)
	if cfg.Keys8x8.AppID == "" || cfg.Keys8x8.KeyID == "" || cfg.Keys8x8.PrivateKey == "" {
		fmt.Fprintln(os.Stderr, "error: 8x8 keys not configured")
		os.Exit(1)
	}
	privKey := parsePrivateKey(cfg.Keys8x8.PrivateKey)
	links, err := server.NewModeratorLinksLog(stateDir())
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: opening moderator links log: %v\n", err)
		os.Exit(1)
	}
	srv := server.New(server.Config{
		BaseURL:       cfg.BaseURL,
		AppID:         cfg.Keys8x8.AppID,
		Logger:        logger,
		JWTPrivateKey: privKey,
		JWTKeyID:      cfg.Keys8x8.KeyID,
		ModeratorAuth: buildModeratorAuthConfig(cfg, links),
	})
	return srv, cfg
}

func buildModeratorAuthConfig(cfg appConfig, links *server.ModeratorLinksLog) server.ModeratorAuthConfig {
	rooms := make(map[string][]string, len(cfg.ModeratorAuth.Rooms))
	for room, entry := range cfg.ModeratorAuth.Rooms {
		rooms[room] = append([]string{}, entry.Moderators...)
	}
	magicTTL := parseDurationDefault(cfg.ModeratorAuth.MagicLinkTTL, 15*time.Minute)
	jwtTTL := parseDurationDefault(cfg.ModeratorAuth.ModeratorJWTTTL, 2*time.Hour)
	authCfg := server.ModeratorAuthConfig{
		Enabled:         cfg.ModeratorAuth.Enabled,
		MagicLinkTTL:    magicTTL,
		ModeratorJWTTTL: jwtTTL,
		SigningKey:      []byte(cfg.ModeratorAuth.SigningKey),
		FromEmail:       cfg.ModeratorAuth.FromEmail,
		SMTP: server.SMTPConfig{
			Host: cfg.ModeratorAuth.SMTP.Host,
			Port: cfg.ModeratorAuth.SMTP.Port,
			User: cfg.ModeratorAuth.SMTP.User,
			Pass: cfg.ModeratorAuth.SMTP.Pass,
		},
		Rooms: rooms,
		Links: links,
	}
	if len(authCfg.SigningKey) == 0 && cfg.Keys8x8.PrivateKey != "" {
		authCfg.SigningKey = []byte(cfg.Keys8x8.PrivateKey)
	}
	if authCfg.FromEmail != "" && authCfg.SMTP.Host != "" {
		authCfg.Mailer = server.SMTPModeratorMailer{From: authCfg.FromEmail, SMTP: authCfg.SMTP}
	}
	return authCfg
}

func parseDurationDefault(raw string, fallback time.Duration) time.Duration {
	if raw == "" {
		return fallback
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return fallback
	}
	return d
}

// parsePublicKey decodes a PEM-encoded RSA public key. Used by the registry
// gate to verify moderator-bypass JWTs at request time.
func parsePublicKey(pemStr string) (*rsa.PublicKey, error) {
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, fmt.Errorf("public-key: failed to decode PEM block")
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		// Try the legacy PKCS#1 form as a fallback.
		if rsaKey, rerr := x509.ParsePKCS1PublicKey(block.Bytes); rerr == nil {
			return rsaKey, nil
		}
		return nil, fmt.Errorf("public-key: parse failed: %w", err)
	}
	rsaKey, ok := parsed.(*rsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("public-key: not an RSA key")
	}
	return rsaKey, nil
}

func parsePrivateKey(pemStr string) *rsa.PrivateKey {
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		fmt.Fprintln(os.Stderr, "error: private-key: failed to decode PEM block")
		os.Exit(1)
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: private-key: failed to parse: %v\n", err)
		os.Exit(1)
	}
	rsaKey, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		fmt.Fprintln(os.Stderr, "error: private-key: not an RSA key")
		os.Exit(1)
	}
	return rsaKey
}

// runCreate registers a new meeting room with a start/end window.
func runCreate(args []string) {
	fs := flag.NewFlagSet("create", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, helpCreate)
	}
	configFlag := fs.String("config", "", "comma-separated config files (default: auto from env)")
	roomFlag := fs.String("room", "", "room name (required)")
	onFlag := fs.String("on", "", "all-day meeting date YYYY-MM-DD (no --at needed)")
	fromFlag := fs.String("from", "", "start date YYYY-MM-DD (one-off) or series-start date (recurring)")
	atFlag := fs.String("at", "", "time-of-day HH:MM, local to --tz or UTC; required unless --on")
	tzFlag := fs.String("tz", "", "IANA timezone for --at, e.g. Europe/Dublin (default UTC)")
	durationFlag := fs.String("duration", "", "window length, e.g. 2h or 4:30h")
	openEarlyFlag := fs.String("open-early", "", "recurring: lead before start, e.g. 15m")
	repeatFlag := fs.String("repeat", "", "recurrence: weekly | fortnightly | monthly")
	weekdayFlag := fs.String("weekday", "", "weekday for --repeat (e.g. tue)")
	ordinalFlag := fs.Int("ordinal", 0, "ordinal 1..5 for --repeat monthly")
	endsFlag := fs.String("ends", "", "series end date YYYY-MM-DD (recurring)")
	untilFlag := fs.String("until", "", "removed: use --duration")
	noteFlag := fs.String("note", "", "free-form note")
	fs.Parse(args)

	if *roomFlag == "" {
		fs.Usage()
		os.Exit(2)
	}
	if *untilFlag != "" {
		fmt.Fprintln(os.Stderr, "error: --until is no longer supported; use --duration for the window length")
		os.Exit(2)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	configPaths := *configFlag
	if configPaths == "" {
		configPaths = buildConfigPaths()
	}
	cfg := loadConfig(configPaths, logger)

	duration, err := resolveDuration(*durationFlag, cfg.Meeting.DefaultDuration, 4*time.Hour)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: --duration: %v\n", err)
		os.Exit(2)
	}
	lead, err := resolveDuration(*openEarlyFlag, cfg.Meeting.DefaultOpenEarly, 15*time.Minute)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: --open-early: %v\n", err)
		os.Exit(2)
	}

	loc := time.UTC
	if *tzFlag != "" {
		l, err := time.LoadLocation(*tzFlag)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: --tz: unknown timezone %q\n", *tzFlag)
			os.Exit(2)
		}
		loc = l
	}

	rooms, err := server.NewRoomsLog(stateDir())
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: opening rooms registry: %v\n", err)
		os.Exit(1)
	}
	now := time.Now()

	// All-day one-off shortcut: --on stands alone.
	if *onFlag != "" {
		if *fromFlag != "" || *atFlag != "" || *tzFlag != "" || *repeatFlag != "" {
			fmt.Fprintln(os.Stderr, "error: --on cannot be combined with --from, --at, --tz, or --repeat")
			os.Exit(2)
		}
		day, err := parseDateOnly(*onFlag)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: --on must be a date YYYY-MM-DD: %q\n", *onFlag)
			os.Exit(2)
		}
		createOneOff(rooms, *roomFlag, day, day.Add(24*time.Hour-time.Second), *noteFlag, now)
		return
	}

	// Everything else needs a time-of-day.
	if *atFlag == "" {
		fmt.Fprintln(os.Stderr, "error: --at HH:MM is required (or use --on for an all-day room)")
		os.Exit(2)
	}
	hh, mm, err := parseTimeOfDay(*atFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: --at: %v\n", err)
		os.Exit(2)
	}

	if *repeatFlag != "" {
		entry, err := buildRecurringEntry(recurringArgs{
			repeat: *repeatFlag, from: *fromFlag, weekday: *weekdayFlag, ordinal: *ordinalFlag,
			ends: *endsFlag, tz: *tzFlag, hh: hh, mm: mm, loc: loc,
			duration: duration, lead: lead, room: *roomFlag, note: *noteFlag, now: now,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(2)
		}
		if err := rooms.Append(entry); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("created %s [%s, anchor %s]\n",
			*roomFlag, *repeatFlag, entry.ValidFrom.UTC().Format(time.RFC3339))
		fmt.Println("next occurrences:")
		printOccurrences(entry.Recurrence.NextOccurrences(entry.ValidFrom, now, 7), *entry.Recurrence, 6)
		return
	}

	// One-off with an explicit start date and time.
	if *fromFlag == "" {
		fmt.Fprintln(os.Stderr, "error: one-off create needs --from <date> (or --on for an all-day room)")
		os.Exit(2)
	}
	day, err := parseDateOnly(*fromFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: --from must be a bare date YYYY-MM-DD (use --at for the time): %q\n", *fromFlag)
		os.Exit(2)
	}
	start := time.Date(day.Year(), day.Month(), day.Day(), hh, mm, 0, 0, loc)
	createOneOff(rooms, *roomFlag, start, start.Add(duration), *noteFlag, now)
}

func createOneOff(rooms *server.RoomsLog, room string, from, until time.Time, note string, now time.Time) {
	if err := server.CreateRoom(rooms, room, from, until, note, now); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("created %s [%s .. %s]\n", room, from.UTC().Format(time.RFC3339), until.UTC().Format(time.RFC3339))
}

// resolveDuration picks the flag value, else the config value, else the
// fallback, parsing the compound duration format.
func resolveDuration(flagVal, cfgVal string, fallback time.Duration) (time.Duration, error) {
	if flagVal != "" {
		return server.ParseDuration(flagVal)
	}
	if cfgVal != "" {
		return server.ParseDuration(cfgVal)
	}
	return fallback, nil
}

type recurringArgs struct {
	repeat, from, weekday, ends, tz, room, note string
	ordinal, hh, mm                             int
	loc                                         *time.Location
	duration, lead                              time.Duration
	now                                         time.Time
}

func buildRecurringEntry(a recurringArgs) (server.RoomLogEntry, error) {
	rec := server.Recurrence{Duration: a.duration, Lead: a.lead, Tz: a.tz}

	wd, err := parseWeekday(a.weekday)
	if err != nil {
		return server.RoomLogEntry{}, err
	}

	// Series start date, in the schedule's zone. Defaults to now when --from
	// is omitted; a bare date otherwise (no time, so no zone contradiction).
	sd := a.now.In(a.loc)
	if a.from != "" {
		d, err := parseDateOnly(a.from)
		if err != nil {
			return server.RoomLogEntry{}, fmt.Errorf("--from must be a bare date YYYY-MM-DD (use --at for the time): %q", a.from)
		}
		sd = d
	}

	var anchor time.Time
	switch strings.ToLower(a.repeat) {
	case "weekly", "fortnightly":
		rec.Kind = server.RecurWeekly
		rec.IntervalWeeks = 1
		if strings.EqualFold(a.repeat, "fortnightly") {
			rec.IntervalWeeks = 2
		}
		// Anchor is the first `wd` on or after the series-start date, at --at in
		// the zone; the weekly step keeps that local wall-clock across DST.
		base := time.Date(sd.Year(), sd.Month(), sd.Day(), a.hh, a.mm, 0, 0, a.loc)
		offset := (int(wd) - int(base.Weekday()) + 7) % 7
		anchor = base.AddDate(0, 0, offset)
	case "monthly":
		if a.ordinal < 1 || a.ordinal > 5 {
			return server.RoomLogEntry{}, fmt.Errorf("--ordinal must be 1..5")
		}
		rec.Kind = server.RecurMonthly
		rec.Ordinal = a.ordinal
		rec.Weekday = wd
		anchor = time.Date(sd.Year(), sd.Month(), sd.Day(), a.hh, a.mm, 0, 0, a.loc)
	default:
		return server.RoomLogEntry{}, fmt.Errorf("--repeat must be weekly, fortnightly, or monthly")
	}

	if a.ends != "" {
		d, err := parseDateOnly(a.ends)
		if err != nil {
			return server.RoomLogEntry{}, fmt.Errorf("--ends must be a date YYYY-MM-DD: %q", a.ends)
		}
		rec.Ends = d.Add(24*time.Hour - time.Second) // inclusive end of that day
	}

	return server.RoomLogEntry{
		Timestamp:  a.now,
		Room:       a.room,
		Status:     server.RoomCreated,
		ValidFrom:  anchor,
		Note:       a.note,
		Recurrence: &rec,
	}, nil
}

func parseWeekday(s string) (time.Weekday, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "sun", "sunday":
		return time.Sunday, nil
	case "mon", "monday":
		return time.Monday, nil
	case "tue", "tues", "tuesday":
		return time.Tuesday, nil
	case "wed", "weds", "wednesday":
		return time.Wednesday, nil
	case "thu", "thur", "thurs", "thursday":
		return time.Thursday, nil
	case "fri", "friday":
		return time.Friday, nil
	case "sat", "saturday":
		return time.Saturday, nil
	}
	return 0, fmt.Errorf("unknown weekday %q", s)
}

func parseTimeOfDay(s string) (hour, minute int, err error) {
	t, err := time.Parse("15:04", strings.TrimSpace(s))
	if err != nil {
		return 0, 0, fmt.Errorf("--at must be HH:MM: %w", err)
	}
	return t.Hour(), t.Minute(), nil
}

// printOccurrences prints up to limit occurrences (start .. end), in the zone
// when set plus UTC, and a trailing note when more remain.
func printOccurrences(occ []time.Time, rec server.Recurrence, limit int) {
	loc := time.UTC
	if rec.Tz != "" {
		if l, err := time.LoadLocation(rec.Tz); err == nil {
			loc = l
		}
	}
	more := false
	if len(occ) > limit {
		occ = occ[:limit]
		more = true
	}
	for _, start := range occ {
		end := start.Add(rec.Duration)
		if rec.Tz != "" {
			fmt.Printf("  %s .. %s  (%s .. %s UTC)\n",
				start.In(loc).Format("Mon 2006-01-02 15:04 MST"),
				end.In(loc).Format("15:04 MST"),
				start.UTC().Format("15:04"),
				end.UTC().Format("15:04"))
		} else {
			fmt.Printf("  %s .. %s UTC\n",
				start.UTC().Format("Mon 2006-01-02 15:04"),
				end.UTC().Format("15:04"))
		}
	}
	if more {
		fmt.Println("  ... and more occurrences")
	}
}

func parseDateOnly(raw string) (time.Time, error) {
	t, err := time.Parse("2006-01-02", raw)
	if err != nil {
		return time.Time{}, err
	}
	return t.UTC(), nil
}

// runCancel cancels a previously-registered room.
func runCancel(args []string) {
	fs := flag.NewFlagSet("cancel", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, helpCancel)
	}
	configFlag := fs.String("config", "", "comma-separated config files (default: auto from env)")
	roomFlag := fs.String("room", "", "room name (required)")
	noteFlag := fs.String("note", "", "free-form note")
	fs.Parse(args)

	if *roomFlag == "" {
		fs.Usage()
		os.Exit(2)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	configPaths := *configFlag
	if configPaths == "" {
		configPaths = buildConfigPaths()
	}
	_ = loadConfig(configPaths, logger)

	rooms, err := server.NewRoomsLog(stateDir())
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: opening rooms registry: %v\n", err)
		os.Exit(1)
	}
	if err := server.CancelRoom(rooms, *roomFlag, *noteFlag, time.Now()); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("cancelled %s\n", *roomFlag)
}

// runList prints registered rooms filtered by status.
func runList(args []string) {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, helpList)
	}
	configFlag := fs.String("config", "", "comma-separated config files (default: auto from env)")
	filterFlag := fs.String("filter", "current", "current | all | active | upcoming | past | cancelled")
	roomFlag := fs.String("room", "", "list a room's upcoming occurrences instead of all rooms")
	countFlag := fs.Int("count", 6, "max occurrences to show with --room")
	fs.Parse(args)

	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	configPaths := *configFlag
	if configPaths == "" {
		configPaths = buildConfigPaths()
	}
	_ = loadConfig(configPaths, logger)

	rooms, err := server.NewRoomsLog(stateDir())
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: opening rooms registry: %v\n", err)
		os.Exit(1)
	}
	now := time.Now()

	if *roomFlag != "" {
		listOccurrences(rooms, *roomFlag, *countFlag, now)
		return
	}

	entries, err := server.ListRooms(rooms, server.RoomFilter(*filterFlag), now)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	for _, e := range entries {
		if e.Recurrence != nil {
			fmt.Printf("%-30s  %-10s  %s (anchor %s)  %s\n",
				e.Room, e.Status, e.Recurrence.Kind, e.ValidFrom.UTC().Format(time.RFC3339), e.Note)
			continue
		}
		from := "-"
		until := "-"
		if !e.ValidFrom.IsZero() {
			from = e.ValidFrom.UTC().Format(time.RFC3339)
		}
		if !e.ValidUntil.IsZero() {
			until = e.ValidUntil.UTC().Format(time.RFC3339)
		}
		fmt.Printf("%-30s  %-10s  %s  ..  %s  %s\n", e.Room, e.Status, from, until, e.Note)
	}
}

// listOccurrences prints a room's upcoming occurrences (recurring), or its
// window (one-off), or a not-found note.
func listOccurrences(rooms *server.RoomsLog, room string, count int, now time.Time) {
	entry := rooms.LatestByRoom(room)
	if entry == nil || entry.Status != server.RoomCreated {
		fmt.Printf("no active or upcoming meeting for %q\n", room)
		return
	}
	if entry.Recurrence != nil {
		occ := entry.Recurrence.NextOccurrences(entry.ValidFrom, now, count+1)
		if len(occ) == 0 {
			fmt.Printf("no upcoming occurrences for %q\n", room)
			return
		}
		fmt.Printf("upcoming occurrences of %s:\n", room)
		printOccurrences(occ, *entry.Recurrence, count)
		return
	}
	if now.After(entry.ValidUntil) {
		fmt.Printf("no upcoming occurrences for %q\n", room)
		return
	}
	fmt.Printf("%s  %s  ..  %s\n", room,
		entry.ValidFrom.UTC().Format(time.RFC3339), entry.ValidUntil.UTC().Format(time.RFC3339))
}

// stateDir returns the directory the rooms registry (and other state files)
// live in. Mirrors the convention used by runServe.
func stateDir() string {
	if d := os.Getenv("STATE_DIRECTORY"); d != "" {
		return d
	}
	return "."
}

// buildConfigPaths builds the config file chain from environment variables.
// Load order: defaults -> host config -> secrets (each overrides the previous).
func buildConfigPaths() string {
	defaultsPath := "config/defaults.yaml"
	if cp := os.Getenv("CONFIG_PATH"); cp != "" {
		defaultsPath = filepath.Join(filepath.Dir(cp), "defaults.yaml")
	}
	paths := []string{defaultsPath}

	if cp := os.Getenv("CONFIG_PATH"); cp != "" {
		paths = append(paths, cp)
	}
	if sp := os.Getenv("SECRETS_PATH"); sp != "" {
		paths = append(paths, sp)
	}

	return strings.Join(paths, ",")
}

func loadConfig(paths string, logger *slog.Logger) appConfig {
	cfg := appConfig{
		Addr: "127.0.0.1:18085",
	}

	for _, p := range strings.Split(paths, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		data, err := os.ReadFile(p)
		if err != nil {
			if os.IsNotExist(err) {
				logger.Info("config: file not found, skipping", "path", p)
				continue
			}
			logger.Error("config: read error", "path", p, "error", err)
			os.Exit(1)
		}
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			logger.Error("config: parse error", "path", p, "error", err)
			os.Exit(1)
		}
		logger.Info("config: loaded", "path", p)
	}

	return cfg
}
