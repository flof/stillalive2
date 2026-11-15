package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

const sessionColumns = `id_hash, user_id, csrf_token, flash_success, flash_error, created_at, expires_at`

func scanSession(row rowScanner) (*Session, error) {
	var (
		s       Session
		userID  sql.NullInt64
		success sql.NullString
		errMsg  sql.NullString
		created int64
		expires int64
	)
	err := row.Scan(&s.IDHash, &userID, &s.CSRFToken, &success, &errMsg, &created, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan session: %w", err)
	}
	if userID.Valid {
		id := userID.Int64
		s.UserID = &id
	}
	s.FlashSuccess = success.String
	s.FlashError = errMsg.String
	s.CreatedAt = time.Unix(created, 0).UTC()
	s.ExpiresAt = time.Unix(expires, 0).UTC()
	return &s, nil
}

// CreateSession stores a new session. IDHash must be the hash of the cookie
// value, never the cookie value itself.
func (s *Store) CreateSession(ctx context.Context, sess *Session) error {
	_, err := s.q.ExecContext(ctx, `INSERT INTO sessions
		(id_hash, user_id, csrf_token, flash_success, flash_error, created_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		sess.IDHash, nullableID(sess.UserID), sess.CSRFToken,
		nullString(sess.FlashSuccess), nullString(sess.FlashError),
		sess.CreatedAt.Unix(), sess.ExpiresAt.Unix())
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	return nil
}

// SessionByIDHash loads a non-expired session.
func (s *Store) SessionByIDHash(ctx context.Context, hash string, now time.Time) (*Session, error) {
	return scanSession(s.q.QueryRowContext(ctx,
		`SELECT `+sessionColumns+` FROM sessions WHERE id_hash = ? AND expires_at > ?`,
		hash, now.Unix()))
}

// AuthenticateSession attaches a user to a session and sets a new expiry.
func (s *Store) AuthenticateSession(ctx context.Context, hash string, userID int64, expiresAt time.Time) error {
	_, err := s.q.ExecContext(ctx,
		`UPDATE sessions SET user_id = ?, expires_at = ? WHERE id_hash = ?`,
		userID, expiresAt.Unix(), hash)
	if err != nil {
		return fmt.Errorf("authenticate session: %w", err)
	}
	return nil
}

// ExtendSession updates the expiry, used for "remember me" logins.
func (s *Store) ExtendSession(ctx context.Context, hash string, expiresAt time.Time) error {
	_, err := s.q.ExecContext(ctx, `UPDATE sessions SET expires_at = ? WHERE id_hash = ?`,
		expiresAt.Unix(), hash)
	if err != nil {
		return fmt.Errorf("extend session: %w", err)
	}
	return nil
}

// SetSessionFlash stores one-shot success and error messages.
func (s *Store) SetSessionFlash(ctx context.Context, hash, success, errMsg string) error {
	_, err := s.q.ExecContext(ctx,
		`UPDATE sessions SET flash_success = ?, flash_error = ? WHERE id_hash = ?`,
		nullString(success), nullString(errMsg), hash)
	if err != nil {
		return fmt.Errorf("set session flash: %w", err)
	}
	return nil
}

// ClearSessionFlash removes one-shot messages after rendering.
func (s *Store) ClearSessionFlash(ctx context.Context, hash string) error {
	_, err := s.q.ExecContext(ctx,
		`UPDATE sessions SET flash_success = NULL, flash_error = NULL WHERE id_hash = ?`, hash)
	if err != nil {
		return fmt.Errorf("clear session flash: %w", err)
	}
	return nil
}

// DeleteSession removes one session.
func (s *Store) DeleteSession(ctx context.Context, hash string) error {
	_, err := s.q.ExecContext(ctx, `DELETE FROM sessions WHERE id_hash = ?`, hash)
	if err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

// DeleteUserSessions removes every session belonging to a user except the
// one identified by keepHash (pass "" to remove all).
func (s *Store) DeleteUserSessions(ctx context.Context, userID int64, keepHash string) error {
	if keepHash == "" {
		_, err := s.q.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, userID)
		if err != nil {
			return fmt.Errorf("delete user sessions: %w", err)
		}
		return nil
	}
	_, err := s.q.ExecContext(ctx,
		`DELETE FROM sessions WHERE user_id = ? AND id_hash <> ?`, userID, keepHash)
	if err != nil {
		return fmt.Errorf("delete user sessions: %w", err)
	}
	return nil
}

// DeleteExpiredSessions prunes old sessions.
func (s *Store) DeleteExpiredSessions(ctx context.Context, now time.Time) (int64, error) {
	res, err := s.q.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ?`, now.Unix())
	if err != nil {
		return 0, fmt.Errorf("delete expired sessions: %w", err)
	}
	return res.RowsAffected()
}

func nullableID(id *int64) any {
	if id == nil {
		return nil
	}
	return *id
}

func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
