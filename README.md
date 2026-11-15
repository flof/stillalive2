# Still Alive v2

A small heartbeat / dead-man's-switch monitor for cronjobs and other scheduled
tasks that cannot be reached from the internet.

Each monitor owns a secret ping URL. The job calls it after every successful
run; if no ping arrives within the configured interval, Still Alive emails the
owner, repeats the alert once a day up to a cap, and sends an all-clear when a
ping arrives again.

This is a rewrite of the Laravel application in `../stillalive`, using:

- **Go 1.27** with the standard library HTTP server
- **templ** for type-safe server-rendered HTML
- **htmx 4.0.0** (vendored, pinned) for the few dynamic interactions
- **SQLite** (pure-Go `modernc.org/sqlite`, no CGO) as the only data store

It is an internal tool: no multi-tenancy beyond per-user monitor ownership, no
admin UI, no public API besides the ping endpoint.

The complete behavioural specification lives in [`docs/spec.md`](docs/spec.md);
deliberate deviations and profile decisions are recorded in
[`DECISIONS.md`](DECISIONS.md).

## Quick start

```bash
cp .env.example .env        # optional; `make run` loads it
make generate               # render templ templates (only needed after editing .templ files)
make run                    # http://localhost:8080
```

Without a `SMTP_HOST`, mail is not sent: messages are written to the log instead
(confirmation, reset, alert and all-clear mails all use the same outbox).
Open `http://localhost:8080/register`, create an account, then create a monitor.

The SQLite database defaults to `./stillalive.db` and is created with its schema
on first start. Change the location with `DATABASE_PATH`.

### Ping a monitor

```bash
curl -s -X POST 'http://localhost:8080/ping/<token>'   # -> 201 OK
```

The token is shown on the monitor's detail page together with a copy-ready curl
command. `GET /ping/<token>` renders a small manual test form.

## Configuration

All configuration is read from environment variables. Defaults are suitable for
local development.

| Variable | Default | Purpose |
|---|---|---|
| `APP_NAME` | `Still Alive` | Brand name in the UI and mails |
| `APP_URL` | `http://localhost:8080` | Public base URL; used in ping URLs and mail links. **Set this in production.** |
| `ADDR` | `:8080` | Listen address |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `DATABASE_PATH` | `stillalive.db` | SQLite file; parent directories are created |
| `SESSION_SECURE` | `false` | Set to `true` when served over HTTPS |
| `SESSION_TTL` | `12h` | Session lifetime (Go duration) |
| `REMEMBER_TTL` | `720h` | Lifetime with "remember me" (30 days) |
| `TOKEN_LENGTH` | `16` | Random bytes per monitor token (minimum 16) |
| `MAX_ALERT_EMAILS` | `5` | Total alert mails per incident |
| `MIN_INTERVAL_MINUTES` | `5` | Smallest allowed monitor interval |
| `REMINDER_INTERVAL` | `24h` | Delay between alert reminders |
| `EVALUATION_INTERVAL` | `1m` | How often monitors are evaluated |
| `PING_RETENTION` | `0` | Delete pings older than this; `0` keeps them forever (v1 parity) |
| `STILLALIVE_POST_PING_URL` | – | Optional URL pinged after every evaluation (instance chaining) |
| `TRUST_PROXY` | `false` | Trust `X-Forwarded-For` / `X-Real-IP` for the stored client IP |
| `SMTP_HOST` | – | Empty means log mode (no mail is sent) |
| `SMTP_PORT` | `587` | SMTP port |
| `SMTP_USERNAME` | – | SMTP username; no auth when empty |
| `SMTP_PASSWORD` | – | SMTP password |
| `SMTP_ENCRYPTION` | `starttls` | `starttls`, `tls` (implicit TLS), or `none` |
| `MAIL_FROM_ADDRESS` | `stillalive@localhost` | Envelope/header sender |
| `MAIL_FROM_NAME` | `Still Alive` | Sender display name |

## Development

```bash
make generate   # templ generate ./...
make test       # go test ./...
make vet        # go vet ./...
make build      # static binary in ./bin/stillalive
```

Requirements: Go 1.27 or newer. The `templ` CLI is invoked via `go run`, so an
extra global install is not needed. Generated `*_templ.go` files are committed,
which means `go build` works without running the generator.

Layout:

```
cmd/stillalive        HTTP server entry point
cmd/import-v1         one-shot importer from the Laravel/MySQL database
internal/app          HTTP handlers, middleware, sessions, validation, static assets
internal/app/views    templ components and view models
internal/checker      due-time evaluation, alert/all-clear lifecycle, self-ping
internal/store        SQLite schema, queries, embedded migrations
internal/mailer       SMTP/log sender and the durable outbox worker
internal/emailmsg     transactional mail content
internal/auth         token, bcrypt and hashing helpers
internal/config       environment configuration
docs/spec.md          behaviour specification inherited from v1
```

Background work runs in the same process: the checker ticks every minute and
the outbox worker every ten seconds. Running multiple application replicas
against one SQLite file is neither supported nor needed at this scale.

## Deploy with Docker

```bash
cp .env.example .env        # set APP_URL, SMTP_*, SESSION_SECURE=true
docker compose up -d --build
```

The image is a static binary on distroless, running as a non-root user, with the
database on a named volume at `/data`. There is no health endpoint; use the
landing page (`GET /`) for liveness checks. Back up the SQLite file (or use
`sqlite3 stillalive.db ".backup '/backups/stillalive-$(date +%F).db'"`) and
monitor the backup job itself with a monitor.

Terminate TLS in front of the application (reverse proxy). Set
`SESSION_SECURE=true` behind HTTPS and `TRUST_PROXY=true` only when a proxy you
control overwrites the forwarded headers.

## Migrating from v1 (Laravel/MySQL)

The v2 schema is close to v1's. Password hashes stay bcrypt-compatible and
**monitor tokens are copied verbatim**, so cronjobs keep working. Plan a short
maintenance window; the importer does not dual-write.

1. **Stop v1 writes.** Stop the web container, the per-minute scheduler and the
   queue worker so no pings or alert state change during the copy. Take a
   `mysqldump` backup first.
2. **Import into an empty v2 database.**

   ```bash
   V1_DSN='stillalive:secret@tcp(127.0.0.1:3306)/stillalive' \
   DATABASE_PATH=/srv/stillalive/stillalive.db \
   ./bin/import-v1 -pings=true
   ```

   The importer copies users (including confirmation state and bcrypt hashes),
   monitors (including `is_overdue`, `alert_email_count`, `last_alert_email_at`
   and due times), and optionally the full ping history. It refuses to run
   against a non-empty v2 database and verifies every token after the import.
   Use `-pings=false` if the history is too large to be worth copying; monitor
   detail pages then show zero recorded pings until new ones arrive.
3. **Start v2** with the same public hostname so existing `POST /ping/{token}`
   URLs keep resolving. If the hostname changes, tokens remain valid but the
   URL in your cronjobs has to be updated.
4. **Verify.** For a sample of monitors, check that
   `curl -i -X POST https://<host>/ping/<token>` returns `201 OK`, that the
   monitor detail page shows the expected interval and last ping, and that a
   deliberately disabled monitor still returns `404`.
5. **Log in.** v1 PHP sessions are not migrated, so users log in again with
   their existing password. In-flight password-reset links are not migrated;
   users can request new ones.

The importer is idempotent only in the sense that it refuses a non-empty target.
To retry, remove the half-written SQLite file and run it again.

## Behaviour notes

- The ping contract is fixed: `POST /ping/{token}` → `201` `OK`; disabled
  monitor → `404` `Monitor exists but is not enabled`; unknown token → `404`.
  CSRF does not apply to this endpoint.
- A monitor only starts being evaluated after its first ping. Until then it
  displays "Waiting for first ping" and never alerts (v1 parity).
- Alert mails are only delivered to confirmed addresses, but the alert state
  still flips for unconfirmed users (v1 parity).
- All times are stored in UTC as Unix seconds; the user's timezone only affects
  display. Mail timestamps are UTC (v1 parity).
- Monitor mutations are POST-only (`/monitors/{id}/edit`, `/toggle`, `/delete`);
  the ping form and delete confirmation use small htmx fragments.
- Ownership is enforced on every monitor route: foreign monitors return `404`.
