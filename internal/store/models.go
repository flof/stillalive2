package store

import "time"

// User is a registered account.
type User struct {
	ID                int64
	Name              string
	Email             string
	PasswordHash      string
	Timezone          string
	AcceptsTerms      bool
	EmailConfirmedAt  *time.Time
	EmailConfirmToken string
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// HasConfirmedEmail reports whether alert mail may be sent to the user.
func (u *User) HasConfirmedEmail() bool { return u != nil && u.EmailConfirmedAt != nil }

// Monitor is a watched task.
type Monitor struct {
	ID               int64
	UserID           int64
	Name             string
	MaxPingInterval  int // minutes
	Token            string
	LastPingAt       *time.Time
	IsOverdue        bool
	LastAlertEmailAt *time.Time
	AlertEmailCount  int
	NextPingDueAt    *time.Time
	Enabled          bool
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// Overdue reports whether the monitor is currently late. A monitor that has
// never pinged counts as overdue for display purposes, mirroring v1.
func (m *Monitor) Overdue(now time.Time) bool {
	if m.LastPingAt == nil {
		return true
	}
	return now.Sub(*m.LastPingAt) > time.Duration(m.MaxPingInterval)*time.Minute
}

// Due reports whether the next ping is in the past.
func (m *Monitor) Due(now time.Time) bool {
	return m.NextPingDueAt != nil && m.NextPingDueAt.Before(now)
}

// NextDue returns the due time for a ping received at pingAt.
func (m *Monitor) NextDue(pingAt time.Time) time.Time {
	return pingAt.Add(time.Duration(m.MaxPingInterval) * time.Minute)
}

// Session is a server-side login session.
type Session struct {
	IDHash       string
	UserID       *int64
	CSRFToken    string
	FlashSuccess string
	FlashError   string
	CreatedAt    time.Time
	ExpiresAt    time.Time
}

// Authed reports whether the session belongs to a user.
func (s *Session) Authed() bool { return s != nil && s.UserID != nil }

// Email is one message in the outbox.
type Email struct {
	ID            int64
	To            string
	Subject       string
	BodyHTML      string
	BodyText      string
	Status        string
	Attempts      int
	LastError     string
	NextAttemptAt time.Time
	CreatedAt     time.Time
	SentAt        *time.Time
}

// Candidate is a monitor joined with its owner, used by the checker.
type Candidate struct {
	Monitor
	UserEmail     string
	UserName      string
	UserTimezone  string
	UserConfirmed bool
}
