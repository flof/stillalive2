package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

const monitorColumns = `m.id, m.user_id, m.name, m.max_ping_interval, m.token,
	m.last_ping_at, m.is_overdue, m.last_alert_email_at, m.alert_email_count,
	m.next_ping_due_at, m.enabled, m.created_at, m.updated_at`

func scanMonitor(row rowScanner) (*Monitor, error) {
	var (
		m         Monitor
		lastPing  sql.NullInt64
		overdue   int
		lastAlert sql.NullInt64
		due       sql.NullInt64
		enabled   int
		created   int64
		updated   int64
	)
	err := row.Scan(&m.ID, &m.UserID, &m.Name, &m.MaxPingInterval, &m.Token,
		&lastPing, &overdue, &lastAlert, &m.AlertEmailCount, &due, &enabled, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan monitor: %w", err)
	}
	m.LastPingAt = timeFromNull(lastPing)
	m.IsOverdue = overdue != 0
	m.LastAlertEmailAt = timeFromNull(lastAlert)
	m.NextPingDueAt = timeFromNull(due)
	m.Enabled = enabled != 0
	m.CreatedAt = time.Unix(created, 0).UTC()
	m.UpdatedAt = time.Unix(updated, 0).UTC()
	return &m, nil
}

// CreateMonitor inserts a monitor and returns its id.
func (s *Store) CreateMonitor(ctx context.Context, m *Monitor, now time.Time) (int64, error) {
	res, err := s.q.ExecContext(ctx, `INSERT INTO monitors
		(user_id, name, max_ping_interval, token, enabled, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		m.UserID, m.Name, m.MaxPingInterval, m.Token, boolInt(m.Enabled), now.Unix(), now.Unix())
	if isUniqueViolation(err) {
		return 0, ErrDuplicate
	}
	if err != nil {
		return 0, fmt.Errorf("create monitor: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("monitor id: %w", err)
	}
	return id, nil
}

// MonitorsByUser returns the user's monitors, newest first.
func (s *Store) MonitorsByUser(ctx context.Context, userID int64) ([]Monitor, error) {
	rows, err := s.q.QueryContext(ctx, `SELECT `+monitorColumns+`
		FROM monitors m WHERE m.user_id = ? ORDER BY m.created_at DESC, m.id DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("list monitors: %w", err)
	}
	defer rows.Close()
	out := make([]Monitor, 0, 8)
	for rows.Next() {
		m, err := scanMonitor(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *m)
	}
	return out, rows.Err()
}

// MonitorByID loads a monitor owned by userID. Monitors of other users are
// reported as not found, so callers cannot distinguish them.
func (s *Store) MonitorByID(ctx context.Context, id, userID int64) (*Monitor, error) {
	return scanMonitor(s.q.QueryRowContext(ctx,
		`SELECT `+monitorColumns+` FROM monitors m WHERE m.id = ? AND m.user_id = ?`, id, userID))
}

// MonitorByToken loads a monitor by its ping token.
func (s *Store) MonitorByToken(ctx context.Context, token string) (*Monitor, error) {
	return scanMonitor(s.q.QueryRowContext(ctx,
		`SELECT `+monitorColumns+` FROM monitors m WHERE m.token = ?`, token))
}

// UpdateMonitorSettings updates name, interval and enabled. When a monitor is
// disabled its alert state is reset; when it is enabled again the due time is
// recomputed from the last ping (fixing a v1 gap where re-enabled monitors
// stayed silent until the next ping).
func (s *Store) UpdateMonitorSettings(ctx context.Context, m *Monitor, name string, interval int, enabled bool, now time.Time) error {
	var (
		res sql.Result
		err error
	)
	if enabled {
		var due any
		if m.LastPingAt != nil {
			due = m.LastPingAt.Add(time.Duration(interval) * time.Minute).Unix()
		}
		res, err = s.q.ExecContext(ctx, `UPDATE monitors SET
			name = ?, max_ping_interval = ?, enabled = 1,
			next_ping_due_at = ?, updated_at = ?
			WHERE id = ? AND user_id = ?`,
			name, interval, due, now.Unix(), m.ID, m.UserID)
	} else {
		res, err = s.q.ExecContext(ctx, `UPDATE monitors SET
			name = ?, max_ping_interval = ?, enabled = 0,
			next_ping_due_at = NULL, is_overdue = 0,
			last_alert_email_at = NULL, alert_email_count = 0, updated_at = ?
			WHERE id = ? AND user_id = ?`,
			name, interval, now.Unix(), m.ID, m.UserID)
	}
	if err != nil {
		return fmt.Errorf("update monitor: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("update monitor result: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// ToggleMonitor flips the enabled flag and returns the new state.
func (s *Store) ToggleMonitor(ctx context.Context, id, userID int64, now time.Time) (bool, error) {
	var enabled bool
	err := s.InTx(ctx, func(tx *Store) error {
		m, err := tx.MonitorByID(ctx, id, userID)
		if err != nil {
			return err
		}
		enabled = !m.Enabled
		return tx.UpdateMonitorSettings(ctx, m, m.Name, m.MaxPingInterval, enabled, now)
	})
	return enabled, err
}

// RecordPing persists a ping and advances the monitor's due time atomically.
func (s *Store) RecordPing(ctx context.Context, m *Monitor, clientIP string, now time.Time) (int64, error) {
	var pingID int64
	err := s.InTx(ctx, func(tx *Store) error {
		res, err := tx.q.ExecContext(ctx,
			`INSERT INTO pings (monitor_id, client_ip, created_at) VALUES (?, ?, ?)`,
			m.ID, clientIP, now.Unix())
		if err != nil {
			return fmt.Errorf("insert ping: %w", err)
		}
		pingID, err = res.LastInsertId()
		if err != nil {
			return fmt.Errorf("ping id: %w", err)
		}
		due := m.NextDue(now)
		_, err = tx.q.ExecContext(ctx,
			`UPDATE monitors SET last_ping_at = ?, next_ping_due_at = ?, updated_at = ? WHERE id = ?`,
			now.Unix(), due.Unix(), now.Unix(), m.ID)
		if err != nil {
			return fmt.Errorf("advance monitor: %w", err)
		}
		return nil
	})
	return pingID, err
}

// DeleteMonitor deletes a monitor owned by userID.
func (s *Store) DeleteMonitor(ctx context.Context, id, userID int64) error {
	res, err := s.q.ExecContext(ctx, `DELETE FROM monitors WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return fmt.Errorf("delete monitor: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete monitor result: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// DueCandidates returns enabled monitors whose due time has passed, joined
// with their owner. Monitors that have never pinged have no due time and are
// therefore excluded, matching v1.
func (s *Store) DueCandidates(ctx context.Context, now time.Time) ([]Candidate, error) {
	return s.candidates(ctx, `m.enabled = 1 AND m.next_ping_due_at IS NOT NULL AND m.next_ping_due_at < ?`, now)
}

// OverdueCandidates returns monitors in the alerting state whose due time has
// moved into the future again (the ping that recovered them).
func (s *Store) OverdueCandidates(ctx context.Context, now time.Time) ([]Candidate, error) {
	return s.candidates(ctx, `m.enabled = 1 AND m.is_overdue = 1 AND m.next_ping_due_at IS NOT NULL AND m.next_ping_due_at > ?`, now)
}

func (s *Store) candidates(ctx context.Context, where string, now time.Time) ([]Candidate, error) {
	rows, err := s.q.QueryContext(ctx, `SELECT `+monitorColumns+`,
		u.email, u.name, u.timezone, u.email_confirmed_at
		FROM monitors m JOIN users u ON u.id = m.user_id
		WHERE `+where+` ORDER BY m.id`, now.Unix())
	if err != nil {
		return nil, fmt.Errorf("list candidates: %w", err)
	}
	defer rows.Close()

	out := make([]Candidate, 0, 8)
	for rows.Next() {
		var (
			c         Candidate
			lastPing  sql.NullInt64
			overdue   int
			lastAlert sql.NullInt64
			due       sql.NullInt64
			enabled   int
			created   int64
			updated   int64
			confirmed sql.NullInt64
		)
		err := rows.Scan(&c.ID, &c.UserID, &c.Name, &c.MaxPingInterval, &c.Token,
			&lastPing, &overdue, &lastAlert, &c.AlertEmailCount, &due, &enabled,
			&created, &updated, &c.UserEmail, &c.UserName, &c.UserTimezone, &confirmed)
		if err != nil {
			return nil, fmt.Errorf("scan candidate: %w", err)
		}
		c.LastPingAt = timeFromNull(lastPing)
		c.IsOverdue = overdue != 0
		c.LastAlertEmailAt = timeFromNull(lastAlert)
		c.NextPingDueAt = timeFromNull(due)
		c.Enabled = enabled != 0
		c.CreatedAt = time.Unix(created, 0).UTC()
		c.UpdatedAt = time.Unix(updated, 0).UTC()
		c.UserConfirmed = confirmed.Valid
		out = append(out, c)
	}
	return out, rows.Err()
}

// RaiseAlert switches a monitor into the alerting state after the first
// missed check-in.
func (s *Store) RaiseAlert(ctx context.Context, id int64, now time.Time) error {
	_, err := s.q.ExecContext(ctx, `UPDATE monitors SET
		is_overdue = 1, alert_email_count = 1, last_alert_email_at = ?, updated_at = ?
		WHERE id = ?`, now.Unix(), now.Unix(), id)
	if err != nil {
		return fmt.Errorf("raise alert: %w", err)
	}
	return nil
}

// BumpAlert records another reminder email.
func (s *Store) BumpAlert(ctx context.Context, id int64, now time.Time) error {
	_, err := s.q.ExecContext(ctx, `UPDATE monitors
		SET alert_email_count = alert_email_count + 1, last_alert_email_at = ?, updated_at = ?
		WHERE id = ?`, now.Unix(), now.Unix(), id)
	if err != nil {
		return fmt.Errorf("bump alert: %w", err)
	}
	return nil
}

// ClearAlert returns a monitor to the healthy state.
func (s *Store) ClearAlert(ctx context.Context, id int64, now time.Time) error {
	_, err := s.q.ExecContext(ctx, `UPDATE monitors SET
		is_overdue = 0, alert_email_count = 0, last_alert_email_at = NULL, updated_at = ?
		WHERE id = ?`, now.Unix(), id)
	if err != nil {
		return fmt.Errorf("clear alert: %w", err)
	}
	return nil
}

// PrunePings deletes ping history older than cutoff. A zero cutoff keeps
// history forever (v1 parity).
func (s *Store) PrunePings(ctx context.Context, cutoff time.Time) (int64, error) {
	if cutoff.IsZero() {
		return 0, nil
	}
	res, err := s.q.ExecContext(ctx, `DELETE FROM pings WHERE created_at < ?`, cutoff.Unix())
	if err != nil {
		return 0, fmt.Errorf("prune pings: %w", err)
	}
	return res.RowsAffected()
}

// CountPings returns the number of stored pings for a monitor.
func (s *Store) CountPings(ctx context.Context, monitorID int64) (int64, error) {
	var n int64
	err := s.q.QueryRowContext(ctx, `SELECT COUNT(*) FROM pings WHERE monitor_id = ?`, monitorID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count pings: %w", err)
	}
	return n, nil
}
