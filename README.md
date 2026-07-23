# meet

Branded 8x8 JaaS (Jitsi as a Service) meeting page. A lightweight Go web app
that serves video meeting rooms with a branded banner, moderator JWT support,
and automatic recording/transcription archival to Nextcloud.

Visitors go to `meet.example.com/workshop-april` and join a room. The moderator
generates a signed JWT URL via the CLI to get admin privileges and can start
recording from the banner. Recordings, transcriptions, and chat logs are
automatically downloaded and uploaded to a Nextcloud WebDAV share.

## Quickstart

```bash
make init          # creates config/localhost.yaml from example
# edit config/localhost.yaml (addr, base_url)
# create secrets/localhost.yaml with 8x8-keys and recording secrets
# see config/localhost.yaml.example for the full secrets structure
make serve         # start the server
make token ROOM=my-room   # generate a moderator URL
```

## Dependencies

- Go 1.24+
- 8x8 JaaS account with API key
- Nextcloud instance with WebDAV access (for recording archival)

## CLI

```
meet                        # start the web server (default: serve)
meet serve --config ...     # start with explicit config files
meet token --room <name>    # generate a moderator JWT URL
meet moderator-link --room <name> --email <addr> --send
                            # send a room-scoped moderator magic link
meet --help                 # show usage
meet --version              # print version
```

### meet-helper (remote SSH shim)

```
meet-helper <host> <subcommand> [args...]
# Examples:
meet-helper light-hugger token --room workshop-april
meet-helper light-hugger moderator-link --room workshop-april \
  --email moderator@example.com --send
meet-helper light-hugger create --room demo \
  --from 2026-05-25T19:00:00Z --until 2026-05-25T21:00:00Z
meet-helper light-hugger list --filter active
meet-helper light-hugger cancel --room demo
```

`meet-helper` ssh's to the named host and runs any `meet` subcommand there,
supplying the canonical deploy-time config cascade automatically.

### Makefile targets

```
make build          # build meet and meet-helper binaries
make serve          # build and run with local config
make token ROOM=x   # generate a moderator JWT URL for a room
make test           # lint + regression tests
make install        # symlink meet and meet-helper to ~/.local/bin
make sync           # git add/commit/pull/push
make release        # tag and push a new version
```

## Config

Three-layer config merged left-to-right:

1. `config/defaults.yaml` - universal baseline (committed)
2. `config/<host>.yaml` via `CONFIG_PATH` env var - host-specific overrides
3. `secrets/<host>.yaml` via `SECRETS_PATH` env var - secrets (age-encrypted for deploy)

The app reads `CONFIG_PATH` and `SECRETS_PATH` environment variables automatically.
The `--config` flag is still supported for explicit override. For local dev,
`secrets/localhost.yaml` (unencrypted, gitignored) is acceptable.

### Config fields

| Field | Location | Description |
|-------|----------|-------------|
| `addr` | config | Bind address (default `127.0.0.1:18085`) |
| `base_url` | config | Public URL, used for banner and token URLs |
| `default_room` | config | Room name when visiting `/` (default `lobby`) |
| `default-moderator-name` | config | Display name for moderator tokens (default `Moderator`) |
| `recording.webdav-path` | config | WebDAV destination folder for recordings |
| `8x8-keys.app-id` | secrets | 8x8 JaaS application ID |
| `8x8-keys.key-id` | secrets | 8x8 API key ID (used as JWT `kid` header) |
| `8x8-keys.private-key` | secrets | RSA private key PEM for JWT signing |
| `8x8-keys.public-key` | secrets | RSA public key PEM (not used at runtime) |
| `recording.webdav-url` | secrets | Nextcloud WebDAV base URL |
| `recording.webdav-user` | secrets | Nextcloud username |
| `recording.webdav-password` | secrets | Nextcloud app password |
| `recording.webhook-token` | secrets | Bearer token for 8x8 webhook auth |
| `moderator-auth` | secrets | Room-to-moderator email relationships and SMTP credentials |

## Features

### Branded meeting rooms

Each URL path creates a meeting room (`/workshop-april`, `/writing-group`).
The banner displays the domain in Special Elite font with the subdomain
highlighted. Root `/` serves the default room (configurable).

### Moderator access

`meet token --room <name>` generates a JWT URL with moderator privileges and
recording enabled. The JWT is passed to the 8x8 JaaS API client-side.

`/<room>/moderator` lets a preapproved moderator request a room-scoped magic
link. The email-to-room relationships and SMTP credentials live in
`secrets/<host>.yaml.age`, loaded at runtime through `SECRETS_PATH`.

### Recording

Moderators see a Record/Stop button in the banner. Recordings use 8x8's
cloud recording infrastructure. After a meeting ends, 8x8 sends webhook
events to `POST /webhook/recording`. The server automatically:

- Downloads recordings, transcriptions, and chat logs to a local staging directory
- Uploads them to Nextcloud via WebDAV with exponential backoff retry (up to 24h)
- On success, moves files to an `uploaded/` directory (kept for 30 days as local backup)
- On failure, files remain in `download/` and are retried on next app startup
- Names files as `{room}_{date}_{time}_{duration}.mp4` (recordings),
  `{room}_{date}_{time}_transcript.{ext}` (transcriptions),
  `{room}_{date}_{time}_chat.{ext}` (chat logs)
- Deduplicates webhook deliveries via idempotency keys
- Logs all webhook events for observability

**Prerequisite:** Register `https://<domain>/webhook/recording` in the 8x8
JaaS admin console with the desired event types and the bearer token from
secrets.

### Tile view

All participants are set to tile view on join. Participants can manually
switch back to speaker view if preferred.

## Important files

| Path | Purpose |
|------|---------|
| `cmd/meet/main.go` | Entrypoint: serve and token subcommands |
| `cmd/meet-helper/main.go` | SSH wrapper for invoking any meet subcommand on a remote host |
| `internal/server/server.go` | HTTP server, routing, domain parsing |
| `internal/server/webhook.go` | Webhook handler, download/upload pipeline |
| `internal/server/static/index.html` | Meeting page template (embedded) |
| `internal/server/static/SpecialElite-Regular.woff2` | Banner font (embedded) |
| `config/defaults.yaml` | Default config |
| `config/<host>.yaml` | Per-host config overrides (see `config/example-host.yaml.example`) |
| `secrets/<host>.yaml.age` | Per-host secrets (see `secrets/example-host.yaml.example`) |
| `docs/8x8-embed.md` | 8x8 JaaS embed API reference |

## Deployment

Deployed via `deploy-app` from the hetzner deploy toolchain. The Makefile
contains local dev targets only - no deploy, SSH, or systemd targets.

## Licence

MIT - Copyright Tadhg O'Brien
