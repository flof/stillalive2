CREATE TABLE users (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    name               TEXT    NOT NULL,
    email              TEXT    NOT NULL COLLATE NOCASE UNIQUE,
    password_hash      TEXT    NOT NULL,
    timezone           TEXT    NOT NULL DEFAULT 'UTC',
    accepts_terms      INTEGER NOT NULL DEFAULT 0,
    email_confirmed_at INTEGER,
    email_confirm_token TEXT,
    created_at         INTEGER NOT NULL,
    updated_at         INTEGER NOT NULL
);

CREATE TABLE sessions (
    id_hash       TEXT    PRIMARY KEY,
    user_id       INTEGER REFERENCES users(id) ON DELETE CASCADE,
    csrf_token    TEXT    NOT NULL,
    flash_success TEXT,
    flash_error   TEXT,
    created_at    INTEGER NOT NULL,
    expires_at    INTEGER NOT NULL
);
CREATE INDEX idx_sessions_expires ON sessions(expires_at);
CREATE INDEX idx_sessions_user ON sessions(user_id);

CREATE TABLE password_resets (
    email      TEXT    NOT NULL,
    token_hash TEXT    NOT NULL,
    expires_at INTEGER NOT NULL,
    created_at INTEGER NOT NULL
);
CREATE INDEX idx_password_resets_email ON password_resets(email);
CREATE INDEX idx_password_resets_token ON password_resets(token_hash);

CREATE TABLE monitors (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id             INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name                TEXT    NOT NULL,
    max_ping_interval   INTEGER NOT NULL, -- minutes
    token               TEXT    NOT NULL UNIQUE,
    last_ping_at        INTEGER,
    is_overdue          INTEGER NOT NULL DEFAULT 0,
    last_alert_email_at INTEGER,
    alert_email_count   INTEGER NOT NULL DEFAULT 0,
    next_ping_due_at    INTEGER,
    enabled             INTEGER NOT NULL DEFAULT 0,
    created_at          INTEGER NOT NULL,
    updated_at          INTEGER NOT NULL
);
CREATE INDEX idx_monitors_user ON monitors(user_id);
CREATE INDEX idx_monitors_due ON monitors(enabled, next_ping_due_at);
CREATE INDEX idx_monitors_last_ping ON monitors(last_ping_at);

CREATE TABLE pings (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    monitor_id INTEGER NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
    client_ip  TEXT    NOT NULL,
    created_at INTEGER NOT NULL
);
CREATE INDEX idx_pings_monitor_created ON pings(monitor_id, created_at DESC);
CREATE INDEX idx_pings_created ON pings(created_at);

CREATE TABLE emails (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    to_address      TEXT    NOT NULL,
    subject         TEXT    NOT NULL,
    body_html       TEXT    NOT NULL,
    body_text       TEXT    NOT NULL,
    status          TEXT    NOT NULL DEFAULT 'pending', -- pending | sent | failed
    attempts        INTEGER NOT NULL DEFAULT 0,
    last_error      TEXT,
    next_attempt_at INTEGER NOT NULL DEFAULT 0,
    created_at      INTEGER NOT NULL,
    sent_at         INTEGER
);
CREATE INDEX idx_emails_outbox ON emails(status, next_attempt_at);
