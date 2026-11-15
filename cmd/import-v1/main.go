// Command import-v1 copies users, monitors, and (optionally) ping history from
// a running Still Alive v1 (Laravel/MySQL) database into a fresh v2 SQLite
// database. Monitor tokens are copied verbatim, so deployed cronjobs keep
// working after the cutover.
//
// Usage:
//
//	V1_DSN='user:pass@tcp(127.0.0.1:3306)/stillalive' \
//	DATABASE_PATH=./stillalive.db \
//	go run ./cmd/import-v1
package main

import (
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"stillalive/internal/auth"
	"stillalive/internal/store"
)

func main() {
	dsn := flag.String("dsn", os.Getenv("V1_DSN"), "MySQL DSN of the v1 database, e.g. user:pass@tcp(host:3306)/stillalive")
	target := flag.String("db", envOr("DATABASE_PATH", "stillalive.db"), "path of the empty v2 SQLite database")
	withPings := flag.Bool("pings", true, "also copy the ping history")
	flag.Parse()

	if *dsn == "" {
		log.Fatal("missing -dsn (or V1_DSN): MySQL DSN of the v1 database")
	}
	if err := run(*dsn, *target, *withPings); err != nil {
		log.Fatalf("import failed: %v", err)
	}
}

func run(dsn, targetPath string, withPings bool) error {
	source, err := sql.Open("mysql", withParams(dsn))
	if err != nil {
		return fmt.Errorf("open v1 database: %w", err)
	}
	defer source.Close()
	if err := source.Ping(); err != nil {
		return fmt.Errorf("connect to v1 database: %w", err)
	}

	db, err := store.Open(targetPath)
	if err != nil {
		return fmt.Errorf("open v2 database: %w", err)
	}
	defer db.Close()

	var existing int
	if err := db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&existing); err != nil {
		return fmt.Errorf("inspect v2 database: %w", err)
	}
	if existing > 0 {
		return errors.New("target database already contains users; import into an empty database")
	}

	users, err := readUsers(source)
	if err != nil {
		return err
	}
	monitors, err := readMonitors(source)
	if err != nil {
		return err
	}
	var pings []v1Ping
	if withPings {
		pings, err = readPings(source)
		if err != nil {
			return err
		}
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin import transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := insertUsers(tx, users); err != nil {
		return err
	}
	if err := insertMonitors(tx, monitors); err != nil {
		return err
	}
	if err := insertPings(tx, pings); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit import: %w", err)
	}

	// Verify that every source token exists verbatim in the target.
	var missing int
	for _, m := range monitors {
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM monitors WHERE token = ?`, m.token).Scan(&n); err != nil {
			return fmt.Errorf("verify token: %w", err)
		}
		if n == 0 {
			missing++
		}
	}
	if missing > 0 {
		return fmt.Errorf("%d monitor tokens are missing after import", missing)
	}

	log.Printf("imported %d users, %d monitors, %d pings; all monitor tokens verified",
		len(users), len(monitors), len(pings))
	return nil
}

// withParams makes sure the MySQL driver returns time.Time values in UTC.
func withParams(dsn string) string {
	params := []string{"parseTime=true", "loc=UTC"}
	separator := "?"
	if strings.Contains(dsn, "?") {
		separator = "&"
	}
	return dsn + separator + strings.Join(params, "&")
}

type v1User struct {
	id               int64
	name             string
	email            string
	password         string
	timezone         string
	acceptsTerms     int
	emailConfirmedAt sql.NullTime
	emailConfirmTok  sql.NullString
	createdAt        time.Time
	updatedAt        time.Time
}

func readUsers(db *sql.DB) ([]v1User, error) {
	rows, err := db.Query(`SELECT id, name, email, password, timezone,
		accepts_terms, email_confirmed_at, email_confirm_token, created_at, updated_at
		FROM users ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("read v1 users: %w", err)
	}
	defer rows.Close()

	var out []v1User
	for rows.Next() {
		var (
			u       v1User
			terms   sql.NullInt64
			confirm sql.NullString
		)
		if err := rows.Scan(&u.id, &u.name, &u.email, &u.password, &u.timezone,
			&terms, &u.emailConfirmedAt, &confirm, &u.createdAt, &u.updatedAt); err != nil {
			return nil, fmt.Errorf("scan v1 user: %w", err)
		}
		if terms.Valid {
			u.acceptsTerms = int(terms.Int64)
		}
		u.emailConfirmTok = confirm
		if u.timezone == "" {
			u.timezone = "UTC"
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

type v1Monitor struct {
	id               int64
	userID           int64
	name             string
	maxPingInterval  int
	token            string
	lastPingAt       sql.NullTime
	isOverdue        int
	lastAlertEmailAt sql.NullTime
	alertEmailCount  int
	nextPingDueAt    sql.NullTime
	enabled          int
	createdAt        time.Time
	updatedAt        time.Time
}

func readMonitors(db *sql.DB) ([]v1Monitor, error) {
	rows, err := db.Query(`SELECT id, user_id, name, max_ping_interval, token,
		last_ping_at, is_overdue, last_alert_email_at, alert_email_count,
		next_ping_due_at, enabled, created_at, updated_at
		FROM monitors ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("read v1 monitors: %w", err)
	}
	defer rows.Close()

	var out []v1Monitor
	for rows.Next() {
		var (
			m       v1Monitor
			token   sql.NullString
			overdue sql.NullInt64
			count   sql.NullInt64
			enabled sql.NullInt64
		)
		if err := rows.Scan(&m.id, &m.userID, &m.name, &m.maxPingInterval, &token,
			&m.lastPingAt, &overdue, &m.lastAlertEmailAt, &count,
			&m.nextPingDueAt, &enabled, &m.createdAt, &m.updatedAt); err != nil {
			return nil, fmt.Errorf("scan v1 monitor: %w", err)
		}
		m.token = token.String
		if m.token == "" {
			generated, err := auth.Token(16)
			if err != nil {
				return nil, fmt.Errorf("generate replacement token: %w", err)
			}
			m.token = generated
			log.Printf("monitor %d has no token in v1; generated a new one", m.id)
		}
		if overdue.Valid {
			m.isOverdue = int(overdue.Int64)
		}
		if count.Valid {
			m.alertEmailCount = int(count.Int64)
		}
		if enabled.Valid {
			m.enabled = int(enabled.Int64)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

type v1Ping struct {
	id        int64
	monitorID int64
	clientIP  string
	createdAt time.Time
}

func readPings(db *sql.DB) ([]v1Ping, error) {
	rows, err := db.Query(`SELECT id, monitor_id, client_ip, created_at
		FROM pings ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("read v1 pings: %w", err)
	}
	defer rows.Close()

	var out []v1Ping
	for rows.Next() {
		var (
			p  v1Ping
			ip sql.NullString
		)
		if err := rows.Scan(&p.id, &p.monitorID, &ip, &p.createdAt); err != nil {
			return nil, fmt.Errorf("scan v1 ping: %w", err)
		}
		p.clientIP = ip.String
		out = append(out, p)
	}
	return out, rows.Err()
}

func insertUsers(tx *sql.Tx, users []v1User) error {
	stmt, err := tx.Prepare(`INSERT INTO users
		(id, name, email, password_hash, timezone, accepts_terms,
		 email_confirmed_at, email_confirm_token, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("prepare user insert: %w", err)
	}
	defer stmt.Close()
	for _, u := range users {
		if _, err := stmt.Exec(u.id, u.name, u.email, u.password, u.timezone, u.acceptsTerms,
			unixNull(u.emailConfirmedAt), nullStringValue(u.emailConfirmTok),
			u.createdAt.Unix(), u.updatedAt.Unix()); err != nil {
			return fmt.Errorf("insert user %d: %w", u.id, err)
		}
	}
	return nil
}

func insertMonitors(tx *sql.Tx, monitors []v1Monitor) error {
	stmt, err := tx.Prepare(`INSERT INTO monitors
		(id, user_id, name, max_ping_interval, token, last_ping_at, is_overdue,
		 last_alert_email_at, alert_email_count, next_ping_due_at, enabled, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("prepare monitor insert: %w", err)
	}
	defer stmt.Close()
	for _, m := range monitors {
		if _, err := stmt.Exec(m.id, m.userID, m.name, m.maxPingInterval, m.token,
			unixNull(m.lastPingAt), m.isOverdue, unixNull(m.lastAlertEmailAt),
			m.alertEmailCount, unixNull(m.nextPingDueAt), m.enabled,
			m.createdAt.Unix(), m.updatedAt.Unix()); err != nil {
			return fmt.Errorf("insert monitor %d: %w", m.id, err)
		}
	}
	return nil
}

func insertPings(tx *sql.Tx, pings []v1Ping) error {
	stmt, err := tx.Prepare(`INSERT INTO pings (id, monitor_id, client_ip, created_at)
		VALUES (?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("prepare ping insert: %w", err)
	}
	defer stmt.Close()
	for _, p := range pings {
		if _, err := stmt.Exec(p.id, p.monitorID, p.clientIP, p.createdAt.Unix()); err != nil {
			return fmt.Errorf("insert ping %d: %w", p.id, err)
		}
	}
	return nil
}

func unixNull(t sql.NullTime) any {
	if !t.Valid {
		return nil
	}
	return t.Time.UTC().Unix()
}

func nullStringValue(s sql.NullString) any {
	if !s.Valid || s.String == "" {
		return nil
	}
	return s.String
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
