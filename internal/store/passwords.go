package store

import (
	"context"
	"fmt"
	"time"
)

// CreatePasswordReset replaces any outstanding reset for the address and
// stores a new token hash.
func (s *Store) CreatePasswordReset(ctx context.Context, email, tokenHash string, expiresAt, now time.Time) error {
	return s.InTx(ctx, func(tx *Store) error {
		if _, err := tx.q.ExecContext(ctx, `DELETE FROM password_resets WHERE email = ?`, email); err != nil {
			return fmt.Errorf("clear password resets: %w", err)
		}
		_, err := tx.q.ExecContext(ctx, `INSERT INTO password_resets
			(email, token_hash, expires_at, created_at) VALUES (?, ?, ?, ?)`,
			email, tokenHash, expiresAt.Unix(), now.Unix())
		if err != nil {
			return fmt.Errorf("create password reset: %w", err)
		}
		return nil
	})
}

// ConsumePasswordReset atomically validates and deletes a reset token.
func (s *Store) ConsumePasswordReset(ctx context.Context, email, tokenHash string, now time.Time) (bool, error) {
	res, err := s.q.ExecContext(ctx, `DELETE FROM password_resets
		WHERE email = ? AND token_hash = ? AND expires_at > ?`,
		email, tokenHash, now.Unix())
	if err != nil {
		return false, fmt.Errorf("consume password reset: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("consume password reset result: %w", err)
	}
	if n > 0 {
		_, _ = s.q.ExecContext(ctx, `DELETE FROM password_resets WHERE email = ?`, email)
	}
	return n > 0, nil
}

// DeleteExpiredPasswordResets prunes old reset requests.
func (s *Store) DeleteExpiredPasswordResets(ctx context.Context, now time.Time) (int64, error) {
	res, err := s.q.ExecContext(ctx, `DELETE FROM password_resets WHERE expires_at <= ?`, now.Unix())
	if err != nil {
		return 0, fmt.Errorf("delete expired password resets: %w", err)
	}
	return res.RowsAffected()
}
