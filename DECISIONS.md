# Decisions

This file records the implementation choices that are not obvious from the code
and the places where v2 deliberately differs from the Laravel application. The
behavioural baseline is [`docs/spec.md`](docs/spec.md).

## Stack and runtime

### Go standard library HTTP server, no framework

The route table fits comfortably in `net/http`'s pattern mux (`GET /monitors/{id}`,
`POST /ping/{token}`). A router dependency would add configuration surface
without removing code. Middleware is explicit: panic recovery, request logging,
security headers, session loading, CSRF, and per-route auth/guest wrappers.

### templ instead of html/template

templ keeps the markup close to HTML and type-checks component calls, which
matters for the many similar forms. Generated `*_templ.go` files are committed,
so `go build` never requires the generator. After editing a `.templ` file run
`make generate` (`go run github.com/a-h/templ/cmd/templ@v0.3.1118 generate ./...`).

### SQLite via `modernc.org/sqlite`

Pure Go, so the binary builds with `CGO_ENABLED=0` and the Docker image can be
distroless. The pool is capped at one connection (`SetMaxOpenConns(1)`) with WAL
and a busy timeout: this removes writer contention and is more than enough for
an internal heartbeat tool. There is no separate database container.

All timestamps are stored as Unix seconds in `INTEGER` columns. This avoids
driver-specific time parsing and makes comparisons unambiguous.

### htmx 4.0.0, vendored

htmx 4.0.0 was released on 2026-08-28. At the time of writing, npm still points
`latest` at 2.x, so using a CDN with a bare version would silently install
htmx 2. The exact 4.0.0 distribution file is committed at
`internal/app/static/htmx.min.js` and served from our own origin. Upgrading is a
one-file replacement plus a review of this list of used features:

- `hx-get` on the monitor rows container, `hx-trigger="every 30s"`, `outerHTML`
- `hx-post`/`hx-target="closest tr"` for toggling a monitor row
- `hx-get` into a slot for the inline delete confirmation
- `hx-post` for resending the confirmation mail, replacing the banner

No inline JavaScript is used anywhere; the CSP therefore allows only same-origin
scripts and styles.

### Server-side sessions in SQLite

Session data (including CSRF tokens and one-shot flash messages) lives in the
`sessions` table; the cookie carries a random 32-byte token and the database
stores only its SHA-256 hash. Benefits over a signed cookie: logout and
"invalidate other sessions" are real deletes, and flash state needs no custom
encoding. Guest sessions are created lazily when a page with a form is rendered,
so ping traffic never writes a session row or sets a cookie.

Sessions are rotated on login and password change (fixation defence). The
session TTL is absolute; there is no idle-sliding renewal, which keeps the model
simple. The "remember me" checkbox selects the longer TTL at login.

### Durable email outbox in SQLite

Every mail is inserted into `emails` and delivered by an in-process worker with
retry/backoff and a parked `failed` state. This mirrors the v1 database queue
without adding Redis or a second process. With no `SMTP_HOST`, the sender writes
the message to the structured log instead, which keeps local development and
tests self-contained.

### In-process checker

v1 used an OS cron entry to dispatch a job every minute. v2 runs a ticker in the
same process, with an immediate first evaluation and a database transaction per
run. Overlapping runs are impossible inside one process; running two application
replicas against the same SQLite file is not supported. Evaluation is
`RunAt(now)` internally so tests can walk the whole alert lifecycle
deterministically.

### Single binary with embedded assets and migrations

`static/*` and `migrations/*.sql` are embedded. Deploying means copying one file
(or running one container) and pointing it at a writable database path.

## URL and form shape

v1 used Laravel resource routes (`PUT /monitors/{id}`, `DELETE /monitors/{id}`),
which HTML forms cannot submit without method spoofing. v2 uses explicit POST
action paths:

| v1 | v2 |
|---|---|
| `PUT/PATCH /monitors/{id}` | `POST /monitors/{id}/edit` |
| `DELETE /monitors/{id}` | `POST /monitors/{id}/delete` |
| (part of the edit form) | `POST /monitors/{id}/toggle` |
| `GET/POST /password/reset` (request form) | `GET/POST /password/forgot` |
| `PUT /profile/update` | `POST /profile/edit` |
| `POST /profile/change_password` | `POST /profile/password` |
| `GET/POST /profile/destroy` | `GET/POST /profile/delete` |
| `GET /profile/request_confirm_email` | `POST /profile/confirm/resend` |

`POST /ping/{token}` and `GET /ping/{token}` keep their exact paths and contract.
`GET /monitors/{id}/delete` renders a full confirmation page for no-JS clients
and a fragment for htmx; only the POST performs the deletion.

## Intentional fixes compared with v1

These change behaviour on purpose. Everything else aims at parity.

1. **Ownership on every monitor route.** v1 checked ownership on delete but not
   consistently on show/edit/update. v2 loads every monitor with
   `WHERE id = ? AND user_id = ?` and returns `404` for foreign rows, so monitor
   existence is not leaked.
2. **`min_interval_minutes` is used for validation.** v1 compared against
   `max_alert_emails` because both happened to be 5. v2 reads the configured
   minimum explicitly.
3. **Re-enabling a monitor recomputes the due time.** v1 left
   `next_ping_due_at` null after disabling, so a monitor enabled again stayed
   silent until its next ping. v2 recomputes from `last_ping_at` on enable and
   still clears the alert state on disable.
4. **Reminder arithmetic is null-safe.** A monitor with `is_overdue = 1` but a
   null `last_alert_email_at` would crash v1's `addHours(24)`; v2 treats it as a
   due reminder.
5. **Password reset redirects to `/login`.** v1 redirected to the non-existent
   `/home` (a 404 after a successful reset).
6. **Remaining-alert copy is accurate.** The footer is computed from
   `max_alert_emails - this_alert_number`, instead of a fixed sentence.
7. **Email confirmation does not log the visitor in.** In v1 the confirmation
   link doubled as a login credential: anyone holding the link became the user.
   v2 confirms the address and redirects to `/monitors` (when already logged in)
   or `/login`, without creating a session for the link holder. The token is
   cleared after successful confirmation.
8. **Passwords are never stored in "remember me" cookies.** There is no remember
   token at all; the session expiry is extended server-side instead.
9. **`TRUST_PROXY` defaults to false.** v1 trusted all proxies and therefore
   `X-Forwarded-For` from any client. The stored client IP is only taken from
   forwarded headers when the deployment opts in.
10. **Sessions and reset tokens are hashed at rest.** A leaked database does not
    yield usable session cookies or reset links. Password-reset tokens expire
    after one hour and are single-use.
11. **Never-pinged monitors are labelled explicitly.** The list shows "Waiting
    for first ping" instead of v1's misleading red "overdue" icon, but they are
    still not evaluated (parity is preserved in the alerting logic).
12. **Disabled-monitor pings return the same 404 but are never stored**, and
    ping ingestion uses a database transaction so the due time can never advance
    without a persisted ping.

## Deliberate parity, not bugs to "fix" later

- The ping endpoint has no authentication beyond the token and no rate limit.
  Changing either would break deployed scripts or require an announcement.
- Alerting is capped per incident (`MAX_ALERT_EMAILS`, default 5), and the
  monitor goes quiet until the next ping.
- Ping history is kept forever by default (`PING_RETENTION=0`).
- Alert/all-clear mail timestamps are UTC, not in the user's timezone.
- A monitor only becomes monitored after its first ping.
- There is still no admin role, public API, pagination, or monitor JSON output.
- Email confirmation is not required to log in or to manage monitors; it only
  gates outgoing alert mail.

## Deferred (not part of this rewrite)

- Login/registration/ping rate limiting and account lockout.
- Token rotation endpoint for ping URLs.
- Ping retention aggregation (`PING_RETENTION` exists but is off by default).
- Monitor-specific maintenance windows, JSON API, teams, billing, status pages.
- Multi-replica deployment (would need a real database or leader election).

## v1 data import

`cmd/import-v1` reads MySQL directly, preserves user IDs and monitor tokens, and
keeps alert state and due times. It intentionally does not import PHP sessions
(users log in again) or in-flight password-reset tokens. Bcrypt hashes are
compatible, including PHP's `$2y$` prefix.
