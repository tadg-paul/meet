// ABOUTME: meet entrypoint. Subcommands: serve (default) starts the web server,
// ABOUTME: token generates a moderator JWT URL for a given room.

package main

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/tadg-paul/meet/internal/server"
	"gopkg.in/yaml.v3"
)

// Version is the build identifier, overridden at link time.
var Version = "dev"

type appConfig struct {
	Addr                 string    `yaml:"addr"`
	BaseURL              string    `yaml:"base_url"`
	DefaultModeratorName string    `yaml:"default-moderator-name"`
	Keys8x8              keys8x8   `yaml:"8x8-keys"`
	Recording            recording `yaml:"recording"`
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
	fmt.Fprintln(os.Stderr, "Usage: meet <command> [options]")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Commands:")
	fmt.Fprintln(os.Stderr, "  serve    Start the web server (default)")
	fmt.Fprintln(os.Stderr, "  token    Generate a moderator JWT URL for a room")
	fmt.Fprintln(os.Stderr, "  create   Register a meeting room with a start/end window")
	fmt.Fprintln(os.Stderr, "  cancel   Cancel a registered meeting room")
	fmt.Fprintln(os.Stderr, "  list     List registered meeting rooms")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Flags:")
	fmt.Fprintln(os.Stderr, "  -h, --help      Show this help")
	fmt.Fprintln(os.Stderr, "  --version       Print version and exit")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Run 'meet <command> -h' for command-specific help.")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "See also: meet-token (SSH wrapper for remote token generation)")
}

func runServe(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: meet [serve] [options]")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Start the meet web server. This is the default command.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Example:")
		fmt.Fprintln(os.Stderr, "  meet")
		fmt.Fprintln(os.Stderr, "  meet serve --config config/defaults.yaml,config/host.yaml,secrets/host.yaml")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Options:")
		fs.PrintDefaults()
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
	// Nix hosts allocate a port at deploy time via ADDR; Ubuntu hosts
	// set addr in config YAML. During the transition both must work.
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

	// Open the recordings log. Tracks CF Stream uploads (#6).
	recordings, err := server.NewRecordingsLog(dataDir)
	if err != nil {
		logger.Error("opening recordings log", "error", err)
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
		fmt.Fprintln(os.Stderr, "Usage: meet token --room <room-name> [options]")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Generate a moderator JWT URL for a meeting room.")
		fmt.Fprintln(os.Stderr, "Requires 8x8-keys (app-id, key-id, private-key) in the config.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Example:")
		fmt.Fprintln(os.Stderr, "  meet token --room workshop-april")
		fmt.Fprintln(os.Stderr, "  meet token --room demo --config config/defaults.yaml,config/host.yaml,secrets/host.yaml")
		fmt.Fprintln(os.Stderr, "  meet token --room demo --name Admin --expiry 4h")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Options:")
		fs.PrintDefaults()
	}
	configFlag := fs.String("config", "", "comma-separated config files (default: auto from env)")
	roomFlag := fs.String("room", "", "room name (required)")
	nameFlag := fs.String("name", "", "display name in the meeting (default from config or \"Moderator\")")
	expiryFlag := fs.Duration("expiry", 2*time.Hour, "token validity duration")
	fs.Parse(args)

	if *roomFlag == "" {
		fmt.Fprintln(os.Stderr, "Usage: meet token --room <room-name> [--config ...] [--name ...] [--expiry ...]")
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
	claims := jwt.MapClaims{
		"aud":  "jitsi",
		"iss":  "chat",
		"sub":  cfg.Keys8x8.AppID,
		"room": "*",
		"iat":  now.Unix(),
		"nbf":  now.Unix(),
		"exp":  now.Add(*expiryFlag).Unix(),
		"context": map[string]interface{}{
			"user": map[string]interface{}{
				"name":      displayName,
				"moderator": "true",
			},
			"features": map[string]interface{}{
				"recording": true,
			},
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = cfg.Keys8x8.KeyID

	signed, err := token.SignedString(privKey)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: failed to sign JWT: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("%s/%s?jwt=%s\n", cfg.BaseURL, *roomFlag, signed)
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
		fmt.Fprintln(os.Stderr, "Usage: meet create --room <name> --from <ts> --until <ts> [--note ...]")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Register a meeting room. The URL becomes joinable from --from until --until")
		fmt.Fprintln(os.Stderr, "for guests; moderator-JWT visits bypass the window.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Timestamps are RFC3339 (e.g. 2026-05-22T19:00:00Z).")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Options:")
		fs.PrintDefaults()
	}
	configFlag := fs.String("config", "", "comma-separated config files (default: auto from env)")
	roomFlag := fs.String("room", "", "room name (required)")
	fromFlag := fs.String("from", "", "valid-from time, RFC3339 (required)")
	untilFlag := fs.String("until", "", "valid-until time, RFC3339 (required)")
	noteFlag := fs.String("note", "", "free-form note")
	fs.Parse(args)

	if *roomFlag == "" || *fromFlag == "" || *untilFlag == "" {
		fs.Usage()
		os.Exit(2)
	}
	from, err := time.Parse(time.RFC3339, *fromFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: --from: %v\n", err)
		os.Exit(2)
	}
	until, err := time.Parse(time.RFC3339, *untilFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: --until: %v\n", err)
		os.Exit(2)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	configPaths := *configFlag
	if configPaths == "" {
		configPaths = buildConfigPaths()
	}
	_ = loadConfig(configPaths, logger) // exercise the cascade even if we only need dataDir

	rooms, err := server.NewRoomsLog(stateDir())
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: opening rooms registry: %v\n", err)
		os.Exit(1)
	}
	if err := server.CreateRoom(rooms, *roomFlag, from, until, *noteFlag, time.Now()); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("created %s [%s .. %s]\n",
		*roomFlag, from.UTC().Format(time.RFC3339), until.UTC().Format(time.RFC3339))
}

// runCancel cancels a previously-registered room.
func runCancel(args []string) {
	fs := flag.NewFlagSet("cancel", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: meet cancel --room <name> [--note ...]")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Cancel a registered meeting room. After cancellation the URL is no longer")
		fmt.Fprintln(os.Stderr, "joinable, even within its original time window.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Options:")
		fs.PrintDefaults()
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
		fmt.Fprintln(os.Stderr, "Usage: meet list [--filter all|active|upcoming|past|cancelled]")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Options:")
		fs.PrintDefaults()
	}
	configFlag := fs.String("config", "", "comma-separated config files (default: auto from env)")
	filterFlag := fs.String("filter", "all", "all | active | upcoming | past | cancelled")
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
	entries, err := server.ListRooms(rooms, server.RoomFilter(*filterFlag), time.Now())
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	for _, e := range entries {
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

// stateDir returns the directory the rooms registry (and other state files)
// live in. Mirrors the convention used by runServe.
func stateDir() string {
	if d := os.Getenv("STATE_DIRECTORY"); d != "" {
		return d
	}
	return "."
}

// buildConfigPaths builds the config file chain from environment
// variables, following the same convention as writeback and golink.
// Load order: defaults -> host config -> secrets (each overrides the previous).
func buildConfigPaths() string {
	paths := []string{"config/defaults.yaml"}

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
