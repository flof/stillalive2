package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

const userColumns = `id, name, email, password_hash, timezone, accepts_terms,
	email_confirmed_at, email_confirm_token, created_at, updated_at`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanUser(row rowScanner) (*User, error) {
	var (
		u         User
		accepts   int
		confirmed sql.NullInt64
		confirm   sql.NullString
		created   int64
		updated   int64
	)
	err := row.Scan(&u.ID, &u.Name, &u.Email, &u.PasswordHash, &u.Timezone, &accepts,
		&confirmed, &confirm, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan user: %w", err)
	}
	u.AcceptsTerms = accepts != 0
	u.EmailConfirmedAt = timeFromNull(confirmed)
	u.EmailConfirmToken = confirm.String
	u.CreatedAt = time.Unix(created, 0).UTC()
	u.UpdatedAt = time.Unix(updated, 0).UTC()
	return &u, nil
}

// CreateUser inserts a user and returns its id.
func (s *Store) CreateUser(ctx context.Context, u *User) (int64, error) {
	now := time.Now().UTC()
	res, err := s.q.ExecContext(ctx, `INSERT INTO users
		(name, email, password_hash, timezone, accepts_terms, email_confirmed_at, email_confirm_token, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		u.Name, u.Email, u.PasswordHash, u.Timezone, boolInt(u.AcceptsTerms),
		nullTime(u.EmailConfirmedAt), u.EmailConfirmToken, now.Unix(), now.Unix())
	if isUniqueViolation(err) {
		return 0, ErrDuplicate
	}
	if err != nil {
		return 0, fmt.Errorf("create user: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("user id: %w", err)
	}
	return id, nil
}

// UserByEmail looks a user up by email address (case-insensitive).
func (s *Store) UserByEmail(ctx context.Context, email string) (*User, error) {
	return scanUser(s.q.QueryRowContext(ctx,
		`SELECT `+userColumns+` FROM users WHERE email = ? COLLATE NOCASE`, email))
}

// UserByID looks a user up by id.
func (s *Store) UserByID(ctx context.Context, id int64) (*User, error) {
	return scanUser(s.q.QueryRowContext(ctx,
		`SELECT `+userColumns+` FROM users WHERE id = ?`, id))
}

// UpdateProfile changes the editable profile fields.
func (s *Store) UpdateProfile(ctx context.Context, id int64, name, timezone string) error {
	_, err := s.q.ExecContext(ctx,
		`UPDATE users SET name = ?, timezone = ?, updated_at = ? WHERE id = ?`,
		name, timezone, time.Now().Unix(), id)
	if err != nil {
		return fmt.Errorf("update profile: %w", err)
	}
	return nil
}

// UpdatePassword replaces the password hash.
func (s *Store) UpdatePassword(ctx context.Context, id int64, hash string) error {
	_, err := s.q.ExecContext(ctx,
		`UPDATE users SET password_hash = ?, updated_at = ? WHERE id = ?`,
		hash, time.Now().Unix(), id)
	if err != nil {
		return fmt.Errorf("update password: %w", err)
	}
	return nil
}

// SetEmailConfirmToken stores a fresh confirmation token.
func (s *Store) SetEmailConfirmToken(ctx context.Context, id int64, token string) error {
	_, err := s.q.ExecContext(ctx,
		`UPDATE users SET email_confirm_token = ?, updated_at = ? WHERE id = ?`,
		token, time.Now().Unix(), id)
	if err != nil {
		return fmt.Errorf("set confirmation token: %w", err)
	}
	return nil
}

// ConfirmEmail marks an address as confirmed when the token matches and the
// address is not yet confirmed.
func (s *Store) ConfirmEmail(ctx context.Context, id int64, token string) (bool, error) {
	if token == "" {
		return false, nil
	}
	res, err := s.q.ExecContext(ctx,
		`UPDATE users SET email_confirmed_at = ?, email_confirm_token = NULL, updated_at = ?
		 WHERE id = ? AND email_confirm_token = ? AND email_confirmed_at IS NULL`,
		time.Now().Unix(), time.Now().Unix(), id, token)
	if err != nil {
		return false, fmt.Errorf("confirm email: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("confirm email result: %w", err)
	}
	return n > 0, nil
}

// DeleteUser removes a user; monitors and pings cascade.
func (s *Store) DeleteUser(ctx context.Context, id int64) error {
	_, err := s.q.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete user: %w", err)
	}
	return nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
