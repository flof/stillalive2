package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// ErrNotFound is returned when a requested row does not exist or is not
// visible to the caller.
var ErrNotFound = errors.New("not found")

// ErrDuplicate is returned when a unique constraint is violated.
var ErrDuplicate = errors.New("duplicate")

// querier is implemented by both *sql.DB and *sql.Tx.
type querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Store wraps the database with the application's queries.
type Store struct {
	q querier
}

// Open opens (and if necessary creates) the SQLite database and applies all
// pending migrations.
func Open(path string) (*sql.DB, error) {
	dsn := path
	if !strings.HasPrefix(path, "file:") {
		dsn = "file:" + path + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
		if path != ":memory:" {
			dsn += "&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)"
		}
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	// A single connection keeps SQLite free of writer contention and is
	// plenty for an internal tool.
	db.SetMaxOpenConns(1)
	db.SetConnMaxLifetime(0)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// New wraps an open database.
func New(db *sql.DB) *Store { return &Store{q: db} }

// InTx runs fn inside a transaction. The Store passed to fn is bound to the
// transaction.
func (s *Store) InTx(ctx context.Context, fn func(*Store) error) error {
	db, ok := s.q.(*sql.DB)
	if !ok {
		return errors.New("store is already inside a transaction")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	if err := fn(&Store{q: tx}); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

func migrate(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version    TEXT PRIMARY KEY,
		applied_at INTEGER NOT NULL
	)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	entries, err := fs.ReadDir(migrationFS, "migrations")
	if err != nil {
		return fmt.Errorf("read migrations: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	for _, name := range names {
		version := strings.SplitN(name, "_", 2)[0]
		var applied int
		err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version = ?`, version).Scan(&applied)
		if err != nil {
			return fmt.Errorf("check migration %s: %w", name, err)
		}
		if applied > 0 {
			continue
		}
		body, err := migrationFS.ReadFile("migrations/" + name)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}
		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("begin migration %s: %w", name, err)
		}
		for _, stmt := range splitStatements(string(body)) {
			if _, err := tx.Exec(stmt); err != nil {
				_ = tx.Rollback()
				return fmt.Errorf("apply migration %s: %w", name, err)
			}
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`, version, time.Now().Unix()); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record migration %s: %w", name, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %s: %w", name, err)
		}
	}
	return nil
}

// splitStatements splits a migration file on semicolons that terminate a
// statement. It is intentionally small: migrations use no triggers, and
// semicolons inside quoted strings are handled.
func splitStatements(sqlText string) []string {
	var (
		out     []string
		current strings.Builder
		quote   rune
	)
	runes := []rune(sqlText)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if quote != 0 {
			current.WriteRune(r)
			if r == quote {
				quote = 0
			}
			continue
		}
		switch r {
		case '\'', '"':
			quote = r
			current.WriteRune(r)
		case '-':
			// Drop line comments; they add nothing to the executed statement.
			if i+1 < len(runes) && runes[i+1] == '-' {
				for i < len(runes) && runes[i] != '\n' {
					i++
				}
				current.WriteRune('\n')
				continue
			}
			current.WriteRune(r)
		case ';':
			if stmt := strings.TrimSpace(current.String()); stmt != "" {
				out = append(out, stmt)
			}
			current.Reset()
		default:
			current.WriteRune(r)
		}
	}
	if stmt := strings.TrimSpace(current.String()); stmt != "" {
		out = append(out, stmt)
	}
	return out
}

func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "unique constraint")
}

func nullTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC().Unix()
}

func timeFromNull(n sql.NullInt64) *time.Time {
	if !n.Valid {
		return nil
	}
	t := time.Unix(n.Int64, 0).UTC()
	return &t
}

func timePtr(t time.Time) *time.Time {
	u := t.UTC()
	return &u
}
