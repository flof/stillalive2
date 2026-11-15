package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// EnqueueEmail adds a message to the outbox.
func (s *Store) EnqueueEmail(ctx context.Context, to, subject, html, text string, now time.Time) (int64, error) {
	res, err := s.q.ExecContext(ctx, `INSERT INTO emails
		(to_address, subject, body_html, body_text, status, attempts, next_attempt_at, created_at)
		VALUES (?, ?, ?, ?, 'pending', 0, ?, ?)`,
		to, subject, html, text, now.Unix(), now.Unix())
	if err != nil {
		return 0, fmt.Errorf("enqueue email: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("email id: %w", err)
	}
	return id, nil
}

// PendingEmails returns messages ready to be delivered.
func (s *Store) PendingEmails(ctx context.Context, now time.Time, limit int) ([]Email, error) {
	rows, err := s.q.QueryContext(ctx, `SELECT id, to_address, subject, body_html, body_text,
		status, attempts, COALESCE(last_error, ''), next_attempt_at, created_at, sent_at
		FROM emails WHERE status = 'pending' AND next_attempt_at <= ?
		ORDER BY id LIMIT ?`, now.Unix(), limit)
	if err != nil {
		return nil, fmt.Errorf("list pending emails: %w", err)
	}
	defer rows.Close()

	out := make([]Email, 0, limit)
	for rows.Next() {
		var (
			e       Email
			next    int64
			created int64
			sent    sql.NullInt64
		)
		err := rows.Scan(&e.ID, &e.To, &e.Subject, &e.BodyHTML, &e.BodyText,
			&e.Status, &e.Attempts, &e.LastError, &next, &created, &sent)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("scan email: %w", err)
		}
		e.NextAttemptAt = time.Unix(next, 0).UTC()
		e.CreatedAt = time.Unix(created, 0).UTC()
		e.SentAt = timeFromNull(sent)
		out = append(out, e)
	}
	return out, rows.Err()
}

// MarkEmailSent records successful delivery.
func (s *Store) MarkEmailSent(ctx context.Context, id int64, now time.Time) error {
	_, err := s.q.ExecContext(ctx, `UPDATE emails
		SET status = 'sent', sent_at = ?, last_error = NULL WHERE id = ?`, now.Unix(), id)
	if err != nil {
		return fmt.Errorf("mark email sent: %w", err)
	}
	return nil
}

// MarkEmailFailed records a delivery failure. When giveUp is true the message
// moves to the failed state and is not retried.
func (s *Store) MarkEmailFailed(ctx context.Context, id int64, reason string, nextAttempt time.Time, giveUp bool) error {
	status := "pending"
	if giveUp {
		status = "failed"
	}
	_, err := s.q.ExecContext(ctx, `UPDATE emails
		SET status = ?, attempts = attempts + 1, last_error = ?, next_attempt_at = ?
		WHERE id = ?`, status, reason, nextAttempt.Unix(), id)
	if err != nil {
		return fmt.Errorf("mark email failed: %w", err)
	}
	return nil
}

// Emails returns recent outbox rows, newest first. Intended for tests and
// operational inspection.
func (s *Store) Emails(ctx context.Context, limit int) ([]Email, error) {
	rows, err := s.q.QueryContext(ctx, `SELECT id, to_address, subject, body_html, body_text,
		status, attempts, COALESCE(last_error, ''), next_attempt_at, created_at, sent_at
		FROM emails ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("list emails: %w", err)
	}
	defer rows.Close()

	out := make([]Email, 0, limit)
	for rows.Next() {
		var (
			e       Email
			next    int64
			created int64
			sent    sql.NullInt64
		)
		if err := rows.Scan(&e.ID, &e.To, &e.Subject, &e.BodyHTML, &e.BodyText,
			&e.Status, &e.Attempts, &e.LastError, &next, &created, &sent); err != nil {
			return nil, fmt.Errorf("scan email: %w", err)
		}
		e.NextAttemptAt = time.Unix(next, 0).UTC()
		e.CreatedAt = time.Unix(created, 0).UTC()
		e.SentAt = timeFromNull(sent)
		out = append(out, e)
	}
	return out, rows.Err()
}
