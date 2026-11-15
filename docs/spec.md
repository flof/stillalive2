# Still Alive — Project Description and Reimplementation Specification

> **Purpose of this document**
> Describe the existing "Still Alive" application completely enough that it can be rebuilt on a
> different technology stack. It documents *behaviour and data*, not the current implementation
> details, and calls out implicit rules, bugs, and constraints that a rewrite must either preserve
> deliberately or fix knowingly.

| | |
|---|---|
| Project | Still Alive (`stillalive.lingu.at`) |
| Source | <https://github.com/flof/stillalive> |
| Type | Web application, dead-man's-switch / cron-job monitoring service |
| Current stack | Laravel 5.6 (PHP), MariaDB, Bootstrap 4 + jQuery, Docker Compose |
| Last code change | 2026-11-12 |
| Original author | Florian Fankhauser |
| License | MIT (inherited from the Laravel skeleton) |

---

## 1. What the application does

Still Alive monitors the *regular execution* of tasks that have no publicly reachable interface,
such as backup scripts, cleanup jobs, or cronjobs on a private server.

It works in the opposite direction of a normal uptime monitor:

1. The user creates a **monitor** with a name and a maximum ping interval (e.g. every 24 hours).
2. The monitored job sends an HTTP **ping** to a secret per-monitor URL after each successful run.
3. If no ping arrives within the configured interval, Still Alive marks the monitor **overdue** and
   emails the owner.
4. When a ping arrives again, the monitor is marked healthy and an **all-clear** email is sent.

The service is a classic "heartbeat" / "dead man's switch": the absence of a signal is the alarm.

### Core value proposition (from the landing page)

- Monitors tasks that cannot be reached from the internet.
- No agent installation: a single HTTP POST is enough.
- Free during beta.

---

## 2. Glossary

| Term | Meaning |
|---|---|
| **User** | Registered account. Owns monitors. Authenticates with email + password. |
| **Monitor** | A watched task. Has a name, a maximum ping interval, an enabled flag, and a secret ping token. |
| **Ping** | An incoming HTTP request proving a monitor's task ran. Persisted with client IP and timestamp. |
| **Ping URL** | `GET/POST /ping/{token}`. Secret, unauthenticated, CSRF-exempt. |
| **Max ping interval** | Allowed time between pings, stored in **minutes**. |
| **Due at** | `last_ping_at + max_ping_interval`. Denormalised into `next_ping_due_at`. |
| **Overdue** | Conceptually "the due time has passed". Persisted as DB column `is_overdue` for alert state; also computed live as a model attribute `overdue` for display. These are two different things with confusingly similar names. |
| **Alert** | The first overdue email for an incident, plus up to N-1 daily reminders. |
| **All-clear** | Email sent when a ping arrives for a monitor that is in the alerting state. |
| **Incident** | One continuous overdue period, from first missed check-in until the next ping. |

---

## 3. Actors

| Actor | Authentication | Capabilities |
|---|---|---|
| Anonymous visitor | none | Landing page, imprint, terms, contact; register; log in; reset password; open a ping URL (GET form or POST). |
| Registered user | session cookie | Everything above plus: manage own monitors, view/edit own profile, change password, confirm email, delete account. |
| Monitored job / script | none (knowledge of the secret token) | POST to its ping URL. |
| Operator | server access | Deploy, run migrations, inspect DB (Adminer in dev), backups. No admin UI exists. |

There is **no admin role and no moderation interface** in the application.

---

## 4. Functional behaviour

### 4.1 Account lifecycle

**Registration** (`GET/POST /register`)

Required fields and validation:

| Field | Rules |
|---|---|
| `name` | required, string, max 255 |
| `email` | required, email format, max 255, unique in `users` |
| `password` | required, string, min 6, must equal `password_confirmation` |
| `terms` | must be accepted (checkbox) |
| `timezone` | required, string, one of the PHP/IANA timezone identifiers |

On success:

- User is created with `accepts_terms = true`, the chosen timezone, and a random
  `email_confirm_token`.
- A **confirmation email** is queued.
- The user is logged in automatically (framework default) and redirected to `/monitors`.

Notable: the email address is *not* verified for login. Unconfirmed users can use the app fully,
but **will not receive any alert or all-clear emails** (see 4.4).

**Login** (`GET/POST /login`): email + password, optional "remember me".
Redirect after login: `/monitors`.

**Logout** (`POST /logout`).

**Password reset** (`GET/POST /password/reset`, `/password/email`): Laravel's default
token-in-email flow, backed by `password_resets`. The reset controller's success redirect points to
`/home`, **which does not exist** (404). A rewrite should redirect to `/monitors` or the profile.

**Email confirmation** (`GET /profile/confirm/{user_id}/{token}`)

- If already confirmed: redirect `/` with error "Your email address has already been confirmed."
- If token mismatch: redirect `/` with error "Invalid token."
- On success: set `email_confirmed_at = now()`, **log the user in if not already authenticated**,
  redirect to `/monitors` with success message.
- The token is **not cleared** after confirmation.
- An authenticated user can request a new confirmation email at
  `GET /profile/request_confirm_email` (queues another email, redirects to `/monitors`).

**Profile** (`GET /profile`, `GET /profile/edit`, `PUT /profile/update`)

- Shows created date, email, name, timezone (dates rendered in the user's timezone).
- Editable: `name` (required, max 255) and `timezone` (required). Email is **not** editable.

**Change password** (`GET /profile/change_password`, `POST /profile/change_password`)

- Requires `current_password` (checked via hash), `new_password` (min 8, confirmed).
- On success the remember token is rotated and a `PasswordReset` event fires.
- Wrong current password: redirect back with flash error "You entered the wrong password."

**Delete account** (`GET /profile/destroy`, `POST /profile/destroy`)

- User must type their exact account name into `nameConfirm`.
- On match: log out, delete the user. Monitors and their pings cascade-delete.
- On mismatch: redirect back with flash error "You entered the wrong name."
- No password confirmation is required.

### 4.2 Monitor management

All monitor routes require authentication. **Ownership enforcement is incomplete in the current
code** (see §12.2).

**List** (`GET /monitors`)

- Shows only the authenticated user's monitors, newest first (`created_at DESC`).
- Columns: status icon, name (link), ping interval (formatted), last ping.
- Last ping cell is green when healthy, red when overdue, empty when disabled; tooltip contains the
  absolute last-ping time. Empty state: "No monitors found".
- No pagination, no search, no filtering.

**Create** (`GET /monitors/create`, `POST /monitors`)

- Form defaults: `enabled = true`, interval `0 hours 5 minutes`.
- Fields: name (required, max 255), `max_ping_interval_hours` (integer 0..200),
  `max_ping_interval_minutes` (integer 0..2000), enabled checkbox.
- The two interval inputs are **added together**: total minutes = hours × 60 + minutes.
- Validation additionally enforces a minimum interval. Intended minimum:
  `config('stillalive.min_interval_minutes')` = 5 minutes. The current code mistakenly compares
  against `config('stillalive.max_alert_emails')` (= 5), which happens to have the same value.
  Error message: "Interval must be at least 5 minutes".
- A token is generated server-side (16 random bytes, base64url-encoded, no padding → 22 chars,
  URL-safe) and stored unique.
- After creation: redirect to the monitor's detail page.

**Detail** (`GET /monitors/{id}`)

- Shows: enabled (Yes/No), formatted interval, created (absolute + relative), ping URL (clickable),
  last ping (absolute + relative, red/green).
- Shows a "How to ping" panel with the exact curl example:
  `curl -s -X POST <pingUrl> > /dev/null`
- Delete button opens a confirmation modal (Bootstrap) that submits a real DELETE request.

**Edit** (`GET /monitors/{id}/edit`, `PUT|PATCH /monitors/{id}`)

- Same fields as create, pre-filled.
- Interval is recomputed from hours + minutes.
- If the monitor is being **disabled**, the alert state is reset:
  `is_overdue = false`, `next_ping_due_at = null`, `last_alert_email_at = null`,
  `alert_email_count = 0`. `last_ping_at` and ping history are kept.
- Changing the interval recalculates `next_ping_due_at` from `last_ping_at` (if a ping exists).

**Delete** (`DELETE /monitors/{id}`)

- Ownership is checked here; foreign users get HTTP 403.
- Hard delete. Pings cascade-delete. Redirect to the monitor list. No undo.

### 4.3 Ping ingestion

`GET /ping/{token}` — public, no CSRF.

- Renders a small "Ping monitor <name>" page explaining that the ping URL was opened with GET and
  offering a "Ping now" button (useful for manual testing).
- If the visitor is logged in, breadcrumbs are shown. If not logged in, the page still renders
  (and exposes the monitor name).
- Unknown token → 404.

`POST /ping/{token}` — public, CSRF-exempt (`/ping/*` is in the CSRF exception list).

Behaviour when the monitor is **enabled**:

1. Persist a `Ping` row with `client_ip` (respecting `X-Forwarded-*` headers, since all proxies are
   trusted) and the current timestamp.
2. Set `monitor.last_ping_at = ping.created_at` (the DB insert time, not request start).
3. Recompute `monitor.next_ping_due_at = last_ping_at + max_ping_interval minutes`.
4. Respond:
   - normal POST → `201 Created` with body `OK`
   - POST containing `from_form=1` (from the GET page) → redirect back to the ping page with flash
     "Ping successfully saved at <local datetime>"

Behaviour when the monitor is **disabled**:

- normal POST → `404 Not Found` with body `Monitor exists but is not enabled`
- `from_form=1` → redirect back with flash "Monitor is not enabled."

Notes:

- No request body, no headers, and no authentication are required. Knowing the token is sufficient.
- Every ping is stored forever; there is no retention or pruning job.
- There is no rate limit on this endpoint.
- Arriving ping does **not** clear the alert state synchronously; the next scheduler run does
  (usually within a minute).

### 4.4 Health evaluation and alert lifecycle

A scheduler runs **every minute** and dispatches a queued job `CheckMonitors`. The job performs two
passes over the `monitors` table, then optionally sends one outbound ping.

**Pass A — raise or repeat alerts**

Candidates: `enabled = true AND next_ping_due_at < now()`.

For each candidate:

- If `is_overdue = false` (first detection of this incident):
  - Send **alert email** if the owner's email is confirmed (if not confirmed, no email is sent at
    all, but the state still flips).
  - Set `is_overdue = true`, `alert_email_count = 1`, `last_alert_email_at = now()`.
- If `is_overdue = true` (already alerting):
  - If `alert_email_count < max_alert_emails` **and**
    `last_alert_email_at + 24h` is in the past:
    - Send another **alert email**.
    - Increment `alert_email_count`, set `last_alert_email_at = now()`.
  - Otherwise do nothing.

With the default `max_alert_emails = 5`, one incident produces at most 5 emails: the initial alert
plus up to 4 daily reminders over the following 4 days. After that the monitor is effectively silent
until a ping arrives.

**Pass B — send all-clear**

Candidates: `enabled = true AND is_overdue = true`.

For each candidate:

- If `next_ping_due_at > now()` (a later ping pushed the due date into the future):
  - Send **all-clear email** if the owner's email is confirmed.
  - Reset `is_overdue = false`, `alert_email_count = 0`, `last_alert_email_at = null`.

**Pass C — optional outbound self-ping**

If `stillalive.post_ping_url` is configured, send an empty HTTP POST to that URL. This lets one
Still Alive instance be watched by another (dogfooding / chain of dead-man's switches). Failures are
ignored.

**State summary**

| `enabled` | `is_overdue` | Meaning | UI icon |
|---|---|---|---|
| false | any | Disabled, no evaluation, no emails | ban icon, grey |
| true | false | Healthy / waiting for due time | check icon, green |
| true | true | Alerting; alert emails until cap, all-clear on ping | warning icon, red |

**Important edge cases**

- A monitor that has **never** received a ping has `next_ping_due_at = NULL` and therefore never
  enters Pass A, so it never alerts, even though its *computed* `overdue` flag is true. A new monitor
  only becomes monitored after its first ping.
- The displayed status (`overdue` accessor) is computed as
  `last_ping_at == NULL` → true, else `minutes since last_ping > max_ping_interval`.
  It is not the same as the persisted `is_overdue` alert state.
- Because Pass B only runs for due/overdue candidates and clears only when the due date is in the
  future, a ping is required; editing the interval alone can move the due date but the job evaluates
  once per minute.
- If `is_overdue = true` but `last_alert_email_at` is null (inconsistent data), the current code
  would fail when adding 24 hours.

### 4.5 Static and legal pages

`GET /` — landing page with a jumbotron and three cards: what it is for, how it works, is it free.
`GET /imprint` — Austrian legal imprint (name and city hard-coded).
`GET /terms` — full terms of service text (governing law: Austria).
`GET /contact` — mailto link `stillalive@lingu.at`.

These are plain content pages with no data dependencies.

---

## 5. Data model

### 5.1 Entity relationship

```mermaid
erDiagram
    users ||--o{ monitors : owns
    monitors ||--o{ pings : records
    users {
        bigint id PK
        string name
        string email UK
        string password
        string remember_token
        boolean accepts_terms
        string timezone
        datetime email_confirmed_at
        string email_confirm_token
        timestamps
    }
    monitors {
        bigint id PK
        bigint user_id FK
        string name
        int max_ping_interval
        string token UK
        timestamp last_ping_at
        boolean is_overdue
        timestamp last_alert_email_at
        int alert_email_count
        timestamp next_ping_due_at
        boolean enabled
        timestamps
    }
    pings {
        bigint id PK
        bigint monitor_id FK
        string client_ip
        timestamps
    }
```

### 5.2 Tables

**`users`**

| Column | Type | Constraints / default | Notes |
|---|---|---|---|
| `id` | unsigned integer, PK | auto-increment | |
| `name` | string(255) | not null | |
| `email` | string(255) | not null, unique | login identifier |
| `password` | string(255) | not null | bcrypt hash |
| `remember_token` | string(100) | nullable | |
| `created_at`, `updated_at` | timestamps | nullable | |
| `accepts_terms` | boolean | default false | set at registration |
| `timezone` | string(255) | default `'UTC'` | IANA identifier, display only |
| `email_confirmed_at` | datetime | nullable | null = unconfirmed |
| `email_confirm_token` | string(255) | nullable | random, not cleared after use |

**`password_resets`** — framework default: `email` (indexed), `token`, `created_at` (nullable).

**`monitors`**

| Column | Type | Constraints / default | Notes |
|---|---|---|---|
| `id` | unsigned integer, PK | auto-increment | |
| `user_id` | unsigned integer | FK → `users.id` ON DELETE CASCADE | owner |
| `name` | string(255) | not null | display name |
| `max_ping_interval` | integer | not null | **minutes** |
| `created_at`, `updated_at` | timestamps | nullable | |
| `token` | string(255) | nullable, unique | ping secret, base64url |
| `last_ping_at` | timestamp | nullable, indexed | time of latest ping |
| `is_overdue` | boolean | not null, default false | persisted alert state |
| `last_alert_email_at` | timestamp | nullable | last alert sent |
| `alert_email_count` | integer | not null | emails in current incident |
| `next_ping_due_at` | timestamp | nullable | derived from last ping + interval |
| `enabled` | boolean | default false | evaluation switch |

**`pings`**

| Column | Type | Constraints / default | Notes |
|---|---|---|---|
| `id` | unsigned integer, PK | auto-increment | |
| `monitor_id` | unsigned integer | FK → `monitors.id` ON DELETE CASCADE | |
| `client_ip` | string(255) | not null | from `X-Forwarded-For` if present |
| `created_at`, `updated_at` | timestamps | nullable | `created_at` is used as `last_ping_at` |

**`jobs`** — framework queue table (`id`, `queue` indexed, `payload`, `attempts`,
`reserved_at`, `available_at`, `created_at`).

**`failed_jobs`** — framework table (`id`, `connection`, `queue`, `payload`, `exception`,
`failed_at` default now).

### 5.3 Derived / computed values

| Value | Definition |
|---|---|
| `overdue` (display) | `last_ping_at IS NULL` → true; else `now - last_ping_at > max_ping_interval` minutes |
| `last_ping_at_formatted` | `---` if null, else relative time ("2 hours ago") |
| `next_ping_due_at` | `last_ping_at + max_ping_interval` minutes; null when no ping or disabled |
| `max_ping_interval_hours` | `floor(max_ping_interval / 60)` |
| `max_ping_interval_minutes` | `max_ping_interval % 60` |
| `max_ping_interval_formatted` | "N minutes" / "N hours" / "H hours M minutes", singular-aware |

All stored timestamps are UTC. User timezones affect **display and email rendering only**; the
scheduler and arithmetic always use UTC.

---

## 6. HTTP interface

### 6.1 Routes

`Auth` = session-authenticated. `Own` = additionally restricted to the resource owner (in a correct
implementation).

| Method | Path | Auth | Purpose | Success response |
|---|---|---|---|---|
| GET | `/` | – | Landing page | 200 HTML |
| GET | `/imprint` | – | Imprint | 200 HTML |
| GET | `/terms` | – | Terms of service | 200 HTML |
| GET | `/contact` | – | Contact page | 200 HTML |
| GET | `/login` | guest | Login form | 200 HTML |
| POST | `/login` | guest | Authenticate | 302 → `/monitors` |
| POST | `/logout` | Auth | Log out | 302 → `/` |
| GET | `/register` | guest | Registration form | 200 HTML |
| POST | `/register` | guest | Create account | 302 → `/monitors` |
| GET | `/password/reset` | guest | Request reset form | 200 HTML |
| POST | `/password/email` | guest | Send reset link | 302 back |
| GET | `/password/reset/{token}` | guest | Reset form | 200 HTML |
| POST | `/password/reset` | guest | Set new password | 302 → `/home` (broken) |
| GET | `/monitors` | Auth | List own monitors | 200 HTML |
| GET | `/monitors/create` | Auth | Create form | 200 HTML |
| POST | `/monitors` | Auth | Create monitor | 302 → `/monitors/{id}` |
| GET | `/monitors/{id}` | Auth/Own | Monitor detail + ping URL | 200 HTML |
| GET | `/monitors/{id}/edit` | Auth/Own | Edit form | 200 HTML |
| PUT/PATCH | `/monitors/{id}` | Auth/Own | Update monitor | 302 → `/monitors/{id}` |
| DELETE | `/monitors/{id}` | Auth/Own | Delete monitor | 302 → `/monitors` |
| GET | `/ping/{token}` | – | Ping form (manual test) | 200 HTML / 404 |
| POST | `/ping/{token}` | – | Record ping | 201 `OK` / 404 |
| GET | `/profile` | Auth | Profile overview | 200 HTML |
| GET | `/profile/edit` | Auth | Edit profile form | 200 HTML |
| PUT | `/profile/update` | Auth | Update name/timezone | 302 → `/profile` |
| GET | `/profile/confirm/{id}/{token}` | – | Confirm email | 302 → `/monitors` or `/` with flash |
| GET | `/profile/request_confirm_email` | Auth | Resend confirmation | 302 → `/monitors` |
| GET | `/profile/destroy` | Auth | Delete-account form | 200 HTML |
| POST | `/profile/destroy` | Auth | Delete account | 302 → `/` |
| GET | `/profile/change_password` | Auth | Change-password form | 200 HTML |
| POST | `/profile/change_password` | Auth | Change password | 302 → `/profile` |

There is **no public JSON API**; `/api` routes exist but are empty. All state changes are
form-encoded HTML flows (plus the ping endpoint, which is usable by scripts).

### 6.2 Ping endpoint contract (the only machine interface)

```
POST /ping/{token}
```

- No authentication, no CSRF token, no body required.
- Success: `201` `text/plain` `OK`.
- Monitor disabled: `404` `text/plain` `Monitor exists but is not enabled`.
- Token unknown: `404`.
- Idempotency: each request creates a ping row and moves the due time; no deduplication.
- Proxy headers are trusted, so `client_ip` reflects `X-Forwarded-For` when present.

A reimplementation must keep this contract, and existing tokens, to avoid breaking deployed cron
jobs.

---

## 7. Background processing

| Concern | Current behaviour |
|---|---|
| Scheduler | OS cron runs `php artisan schedule:run` every minute inside the PHP container. |
| Scheduled work | One job, `CheckMonitors`, scheduled `everyMinute()`, dispatched onto the queue. |
| Queue | Database driver, `jobs` table, worker process `php artisan queue:work --sleep=3 --tries=3`. |
| Failed jobs | `failed_jobs` table; no retry UI. |
| Emails | All mails are queued (`Mail::queue`), therefore delivered by the worker through SMTP. |
| Outbound ping | Synchronous empty POST via cURL inside the scheduled job. |
| Time source | Server/DB clock; all arithmetic in UTC. |
| Concurrency | No locking. Two overlapping runs could double-send; realistically prevented by the 1-minute cadence and a single worker. |

A reimplementation should treat the evaluation as a periodic task (every minute or configurable) with
per-monitor idempotency, and email delivery as an asynchronous, retryable side effect.

---

## 8. Emails

All emails are queued, rendered as Markdown (Laravel mail theme), and sent to the monitor owner /
user. They include an "Open monitor" button linking to `/monitors/{id}`.

| Mail | Trigger | Subject | Body highlights |
|---|---|---|---|
| Confirm email address | Registration; manual resend | `Confirm your email address` | Greeting with name, raw confirmation URL containing user id and token, signature "your team from Still Alive". |
| Monitor alert | First overdue detection; then daily while `alert_email_count < 5` | `Alert for {monitor.name}` | "We have **not** received a ping since {last_ping_at}." Button. Footer states remaining daily alerts, or "This was the last alert email. The monitor is now considered inactive until we receive a ping." |
| Monitor all-clear | Ping arrives while overdue | `All-clear for {monitor.name}` | "We finally received a ping at {last_ping_at}." Button. "We send you an alert email if a future ping is overdue." |

Notes:

- Alert/all-clear emails are sent **only** if `email_confirmed_at` is not null.
- Dates in emails are rendered in UTC (`Y-m-d H:i:s`), not the user's timezone.
- From address/name come from `MAIL_FROM_ADDRESS` / `MAIL_FROM_NAME`.
- The confirmation link is a plain GET URL, so link scanners may confirm addresses.

---

## 9. User interface inventory

Server-rendered Blade templates, Bootstrap 4, open-iconic icon font, Raleway webfont, sticky footer.
JavaScript is only used for Bootstrap behaviours (dropdown, modal); Vue is installed but unused.

| Page | Route | Key elements |
|---|---|---|
| Landing | `/` | Jumbotron, three info cards, footer links |
| Login | `/login` | Email, password, remember me, forgot-password link |
| Register | `/register` | Name, email, password + confirmation, timezone select, terms checkbox |
| Password request / reset | `/password/*` | Framework default forms |
| Monitor list | `/monitors` | Breadcrumbs, card, "Add monitor" button, table (status, name, interval, last ping), empty state |
| Monitor create | `/monitors/create` | Name, interval hours/minutes, enabled checkbox |
| Monitor detail | `/monitors/{id}` | Definition list, ping URL, curl example, edit + delete (modal) |
| Monitor edit | `/monitors/{id}/edit` | Same fields as create |
| Ping form | `/ping/{token}` | Warning that GET was used, "Ping now" button |
| Profile | `/profile` | Created, email, name, timezone; change-password and delete-account buttons |
| Profile edit | `/profile/edit` | Name, timezone select |
| Change password | `/profile/change_password` | Current, new, confirm |
| Delete account | `/profile/destroy` | Warning text, "type your name" confirmation |
| Imprint / Terms / Contact | static | Legal and contact content |

Global layout: navbar (brand, Monitors link when authenticated, user dropdown with Profile/Logout),
flash banners (`success_message`, `error_message`), and a persistent warning banner for users with an
unconfirmed email address ("We cannot send you alert emails for your monitors until you confirm your
email address") including a resend link.

---

## 10. Configuration

### 10.1 Application settings (`config/stillalive.php`)

| Key | Default | Meaning |
|---|---|---|
| `token_length` | `16` | Random bytes per ping token (base64url → 22 chars). |
| `max_alert_emails` | `5` | Max emails per incident, including the first. |
| `min_interval_minutes` | `5` | Intended minimum monitor interval. Currently **not** used (see §12.1). |
| `post_ping_url` | `env(STILLALIVE_POST_PING_URL, '')` | If set, the scheduler POSTs here every minute. |

### 10.2 Environment variables

From `.env.example`:

| Variable | Example | Purpose |
|---|---|---|
| `APP_NAME` | `Still alive` | Branding, mail signature |
| `APP_ENV` | `production` | |
| `APP_KEY` | – | Encryption key (required) |
| `APP_DEBUG` | `false` | |
| `APP_LOG_LEVEL` | `info` | |
| `APP_URL` | `http://localhost:8080` | URL generation (inconsistent with actual ports) |
| `LOG_CHANNEL` / `LOG_LEVEL` | `single` / `debug` | |
| `DB_CONNECTION` | `mysql` | MariaDB |
| `DB_HOST` / `DB_PORT` | `db` / `3306` | |
| `DB_DATABASE` | `stillalive` | |
| `DB_USERNAME` / `DB_PASSWORD` | `stillalive` / `changeme` | |
| `BROADCAST_DRIVER` | `log` | unused |
| `CACHE_DRIVER` | `file` | single-node assumption |
| `SESSION_DRIVER` | `file` | single-node assumption |
| `SESSION_LIFETIME` | `120` | minutes |
| `QUEUE_DRIVER` | `database` | |
| `MAIL_DRIVER` | `smtp` | |
| `MAIL_HOST` / `MAIL_PORT` | – / `587` | |
| `MAIL_USERNAME` / `MAIL_PASSWORD` / `MAIL_ENCRYPTION` | – / – / `tls` | |
| `MAIL_FROM_NAME` / `MAIL_FROM_ADDRESS` | `Still alive` / – | |
| `STILLALIVE_POST_PING_URL` | – (compose only) | Outbound self-ping target |

Framework defaults not in `.env.example`: timezone `UTC`, locale `en`, fallback `en` (no
localisation is actually shipped beyond English framework strings).

---

## 11. Deployment and runtime topology

Docker Compose with profiles.

| Service | Profile | Role |
|---|---|---|
| `db` | always | MariaDB 10.2, named volume `mariadb-data`, credentials from env. |
| `php` | always | PHP 8.2 FPM image; runs **cron** (`cron -f`) with a minutely `artisan schedule:run`; publishes `127.0.0.1:8000` for a manually started `artisan serve` (dev). |
| `queue-worker` | always | Same image, `php artisan queue:work --sleep=3 --tries=3`. Depends on `db`. |
| `node` | `dev` | Node 9 + yarn, source mounted; used for asset builds/watcher. |
| `adminer` | `dev` | DB UI on `127.0.0.1:22346`. |
| `php-fpm` | `prod` | FPM backend for nginx, same image. |
| `nginx` | `prod` | `nginx:1.21.4`, container name `stillalive`, `127.0.0.1:10073 → 80`, serves `/var/www/html/public`, forces `HTTPS on` towards PHP (TLS terminated outside). |
| `backup` | `prod` | Alpine with `tarsnap` + `mariadb-client`; daily cron dumps the DB, uploads to tarsnap, logs the archive name, then POSTs to `STILL_ALIVE_DB_BACKUP_URL` (the app's own ping URL) and deletes the local dump. |

PHP image: `php:8.2.35-fpm`, Composer installed from getcomposer.org, extensions
`pdo_mysql`, `intl`, `opcache`, `gd`, plus `zip`, `unzip`, `wget`, `libicu-dev`, `libpng-dev`, `cron`.

Source is bind-mounted in all app containers; there is no immutable build artifact. Production
deployment is `git pull` + `docker compose --profile prod up -d` + `composer update` +
`config:cache` + `route:cache` + `migrate`, with compiled front-end assets committed to the repo.
A server-local `deployment_script.sh` (git-ignored) is referenced but not in the repo.

Backup service details:

```
mysqldump -u$DB_USERNAME -p$DB_PASSWORD -hdb stillalive > /root/dump.sql
tarsnap -c -f <timestamp>-stillalive-db -C /root/ dump.sql
echo <filename> >> /root/tarsnap-log/backup-files
curl -s -X POST $STILL_ALIVE_DB_BACKUP_URL
rm /root/dump.sql
```

The database name is hard-coded as `stillalive` instead of using `DB_DATABASE`. Tarsnap key, cache,
and logs are local bind mounts and git-ignored.

---

## 12. Observed quirks, bugs, and implicit decisions

These are documented so a rewrite can decide deliberately. "Fix" means the new implementation
should change the behaviour; "preserve" means compatibility or product intent requires keeping it.

### 12.1 Defects worth fixing

1. **Minimum interval validation uses the wrong config key** (`max_alert_emails` instead of
   `min_interval_minutes`). Fix and use the documented key.
2. **Missing authorization on monitor show/edit/update.** Only `destroy` verifies
   `monitor.user_id === auth.id`. Any logged-in user can read or modify another user's monitor by
   ID (the ping URL and monitor name leak; interval/enabled can be changed). Fix with ownership
   checks on every route (policy / scoped queries).
3. **Password reset redirects to `/home`**, which does not exist. Fix.
4. **Never-pinged monitors never alert** because `next_ping_due_at` stays null. Decide explicitly:
   either alert from creation time, or require a first ping and make that clear in the UI.
5. **Null `last_alert_email_at` can crash** the alert pass for inconsistent rows. Make the
   evaluation null-safe.
6. **Unbounded ping growth.** Every ping is stored forever with no retention. Add retention or
   aggregate counters.
7. **Hard-coded database name in the backup script**, ignoring `DB_DATABASE`.
8. **Model factory typo** (`emails` instead of `email`, `str_random`) and only placeholder tests
   exist. No meaningful automated test suite.
9. **Stale/dead files:** `app/Http/Controllers/edit.blade.php`, `routes/destroy.blade.php`,
   unused `UserResource`/`UserCollection`, unused `watson/bootstrap-form` dependency, unused Vue
   dependency, `public/phpinfo.php`.
10. **PHP 8.2 container with Laravel 5.6 / PHPUnit 7** is a version mismatch; Laravel 5.6 targets
    PHP 7.x. The running deployment may rely on tolerating deprecations. The rewrite removes this
    problem.
11. `Ping::monitor()` is declared `hasOne`, but the schema is many-to-one (`belongsTo`). Currently
    unused.
12. The delete-account view ends an `@input_text` component with `@endinput_password` (cosmetic
    template bug).
13. `APP_URL` in `.env.example` (`:8080`) does not match the dev (`:8000`) or prod (`:10073`)
    exposed ports.

### 12.2 Behaviour to preserve

- Ping contract: `POST /ping/{token}` → 201 `OK`, 404 when disabled/unknown, no auth/CSRF/body.
- Token format and existing tokens (so deployed cronjobs keep working after migration).
- Per-user timezone for display; UTC for storage and scheduling.
- Alert cadence: immediate first alert, daily reminders, cap of 5 per incident, all-clear on recovery.
- Alerts suppressed for unconfirmed email addresses, with a persistent UI warning and resend option.
- Monitors only assert "overdue" status once they have pinged at least once (unless the product
  decision in 12.1.4 changes it).
- Manual ping form at the ping URL for GET requests.
- Disabling a monitor resets alert state but keeps history.
- Hard deletes with cascade for monitors and users.
- Static legal pages and the contact address.

---

## 13. Security posture (current state)

- Session-based auth with bcrypt password hashing (framework default), CSRF protection on all web
  forms except `/ping/*`.
- Ping tokens are 128-bit random values in the URL. URLs may leak via logs, browser history, or
  referrers. There is no rotation or revocation control other than deleting/recreating a monitor
  (no "regenerate token" feature).
- No rate limiting on login, registration, or ping.
- No email-verification gate for account usage (only for receiving alerts).
- Broken object-level authorization on monitor read/update (see 12.1.2).
- All proxy headers trusted, so `client_ip` is spoofable by direct clients; this only affects stored
  metadata.
- `APP_DEBUG`, error verbosity, and the presence of Adminer are deployment concerns; Adminer is
  bound to localhost.
- No audit log, no 2FA, no API keys.

A rewrite should add: object-level authorization, token regeneration, per-endpoint rate limiting,
ping token in header or path with rotation, retention policy, and structured audit/alerting for the
monitor service itself.

---

## 14. Scale and non-functional assumptions

- Small scale: a single web node, single DB, single queue worker, file-based sessions/cache.
- The scheduler scans all due/overdue monitors every minute; fine for thousands, not millions,
  without indexing and batching.
- Pings are write-heavy relative to reads (one insert per heartbeat); the `pings` table is the main
  growth vector.
- Alert timing precision is about one minute plus queue latency.
- No multi-tenancy beyond `user_id`; no teams or sharing.
- No SLA, no self-monitoring, no metrics endpoint.

---

## 15. Reimplementation blueprint

### 15.1 Functional scope for v2 (minimum)

Must-have (parity):

- Registration/login/logout/password reset, profile edit, change password, delete account.
- Email confirmation with resend, and alert suppression until confirmed.
- Monitor CRUD with interval in hours + minutes, enabled flag, secret ping URL.
- Ping endpoint with the exact contract and existing-token compatibility.
- Per-minute evaluation with alert/reminder/all-clear lifecycle and the 5-email cap per incident.
- Transactional email templates (confirmation, alert, all-clear) with monitor links.
- Static landing/imprint/terms/contact pages.
- Optional outbound self-ping.
- User timezone display.

Recommended additions:

- Ownership policies everywhere, token rotation, rate limiting, ping retention/aggregation.
- A real monitor detail history (recent pings), incident timeline, and "last N pings" chart.
- "Never pinged" handling and a grace period after creation.
- Scheduled/periodic alerting configuration per monitor, plus downtime windows (maintenance mode).
- JSON API + API tokens for automation, pagination, and export.
- Operational observability: metrics, health endpoint, structured logs, dead-letter handling.

Explicit non-goals (unchanged from v1): active HTTP probing of user sites, status pages, teams,
billing, admin UI, multi-region.

### 15.2 Suggested architecture (stack-agnostic)

| Concern | Requirement | Example choices |
|---|---|---|
| Web/UI | Server-rendered or SPA; session auth; forms with CSRF | Next.js/Remix/SvelteKit, Django, Rails, Laravel 11 |
| Ping ingestion | Stateless, low-latency endpoint, no CSRF, no auth, can absorb bursts | Any HTTP framework; consider a dedicated route/service |
| Database | Relational with FK cascades and unique token | PostgreSQL (preferred), MySQL/MariaDB |
| Scheduler | Runs every minute; must not double-fire | Cron + app scheduler, Kubernetes CronJob, platform cron |
| Job queue | Durable, retryable email delivery and evaluation | Redis + BullMQ/Sidekiq/Celery, DB queue for small scale |
| Email | Transactional provider with retries and bounce handling | Postmark, SES, Mailgun, Resend |
| Config | Environment variables for token length, alert cap, min interval, outbound ping URL | 12-factor config |
| Deployment | Reproducible image + migrations + health check | Docker Compose or managed platform |
| Backups | Daily DB dump, off-site, verified, and monitored by Still Alive itself | Provider snapshots or tarsnap/S3 |

Keep three separable units even in a monolith: **web app**, **ping endpoint**, **evaluation worker**.
That separation makes the heartbeat path independent of UI deploys and lets the worker scale
separately.

### 15.3 Data migration plan

1. Keep the existing schema as the source of truth for backfill.
2. Migrate `users` one-to-one, preserving `id`, email, password hash (or force a reset), timezone,
   `accepts_terms`, and confirmation state.
3. Migrate `monitors` preserving **`token`** (critical: deployed cronjobs must keep working),
   `max_ping_interval`, `enabled`, `last_ping_at`, `is_overdue`, `alert_email_count`,
   `last_alert_email_at`, `next_ping_due_at`, and timestamps.
4. Decide on `pings`: backfill all rows (expensive, unbounded) or only the latest ping per monitor
   plus aggregate counts. If ping history is a v2 feature, backfill with a retention cutoff.
5. Migrate `password_resets` only if reset links in flight matter; usually drop.
6. Run dual-write or a maintenance window: because pings can arrive every few seconds, the safest
   cutover is a short read-only window, or a proxy that dual-writes pings during migration.
7. Verify a sample of monitors: due timestamps, alert state, and that old ping URLs return 201.

### 15.4 Acceptance test checklist

- [ ] Register → receives confirmation email → link confirms → banner disappears.
- [ ] Unconfirmed user: overdue monitor flips state but sends no email; after confirmation, alerts resume.
- [ ] Create monitor with 1 h 30 min → stored interval 90 minutes, due date computed on first ping.
- [ ] `POST /ping/{token}` → 201 `OK`, one row in pings, due date advances, no CSRF needed.
- [ ] `GET /ping/{token}` renders manual ping form; button ping redirects with success message.
- [ ] Disabled monitor: ping returns 404, no ping row, no evaluation.
- [ ] Unknown token: 404 on GET and POST.
- [ ] Missed due time → first alert immediately (confirmed user), monitor shows red.
- [ ] Reminder arrives only after 24 h; total emails per incident never exceed 5.
- [ ] Ping after alerting → all-clear email, state reset, monitor turns green.
- [ ] Editing interval recalculates due date; disabling resets alert state and silences evaluation.
- [ ] Another user cannot read, edit, or delete your monitor (v2 requirement).
- [ ] Delete monitor → pings gone (cascade). Delete account → monitors and pings gone.
- [ ] Password change requires current password; wrong password shows error.
- [ ] Timezone changes alter displayed times but not scheduling.
- [ ] Backups run daily, are stored off-site, and the backup job's own ping keeps a monitor green.
- [ ] Deployed legacy ping URLs still work after migration.

---

## 16. Repository map (current implementation)

```
app/
  Console/Kernel.php            # schedules CheckMonitors every minute
  Helper.php                    # token generation, base64url, outbound POST
  Jobs/CheckMonitors.php        # alert / all-clear / self-ping logic
  LocalDateTimeFormat.php       # timezone-aware display helpers
  Mail/                         # ConfirmEmailAddress, MonitorAlert, MonitorAllClear
  Monitor.php, Ping.php, User.php
  Http/Controllers/             # Monitor, Ping, Profile + framework auth controllers
  Http/Requests/SaveMonitorRequest.php
config/stillalive.php           # token length, alert cap, min interval, outbound ping URL
database/migrations/            # users, monitors, pings, queue tables
resources/views/                # Blade pages, components, emails
resources/assets/               # SCSS + Bootstrap/jQuery entry point (Laravel Mix)
routes/web.php                  # all application routes
docker/                         # PHP and Node images
docker-compose.yml              # dev/prod services, queue worker, backup
cronjobs/laravel-scheduler      # minutely artisan schedule:run
backup/                         # tarsnap + mysqldump backup image and script
```

Unused or legacy artefacts: `app/Http/Controllers/edit.blade.php`, `routes/destroy.blade.php`,
`app/Http/Resources/*`, `watson/bootstrap-form`, Vue, `public/phpinfo.php`.
