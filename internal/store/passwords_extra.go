package store

import (
	"context"
	"fmt"
	"time"
)

// PasswordResetExists reports whether a valid reset request exists for the
// address and token hash. It does not consume the token.
func (s *Store) PasswordResetExists(ctx context.Context, email, tokenHash string, now time.Time) (bool, error) {
	var n int
	err := s.q.QueryRowContext(ctx, `SELECT COUNT(*) FROM password_resets
		WHERE email = ? AND token_hash = ? AND expires_at > ?`,
		email, tokenHash, now.Unix()).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("check password reset: %w", err)
	}
	return n > 0, nil
}
