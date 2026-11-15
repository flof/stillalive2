package checker

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"stillalive/internal/config"
	"stillalive/internal/store"
)

func testStore(t *testing.T) *store.Store {
	t.Helper()
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return store.New(db)
}

func testConfig() config.Config {
	cfg := config.Load()
	cfg.AppURL = "http://example.test"
	cfg.PostPingURL = ""
	cfg.MaxAlertEmails = 5
	cfg.ReminderInterval = 24 * time.Hour
	return cfg
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func mustUser(t *testing.T, st *store.Store, email string, confirmed bool) *store.User {
	t.Helper()
	u := &store.User{
		Name:         "Test User",
		Email:        email,
		PasswordHash: "x",
		Timezone:     "UTC",
		AcceptsTerms: true,
	}
	if confirmed {
		now := time.Now().UTC()
		u.EmailConfirmedAt = &now
	}
	id, err := st.CreateUser(context.Background(), u)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	u.ID = id
	return u
}

func mustMonitor(t *testing.T, st *store.Store, userID int64, token string, interval int, now time.Time) *store.Monitor {
	t.Helper()
	m := &store.Monitor{
		UserID:          userID,
		Name:            "backup",
		MaxPingInterval: interval,
		Token:           token,
		Enabled:         true,
	}
	id, err := st.CreateMonitor(context.Background(), m, now)
	if err != nil {
		t.Fatalf("create monitor: %v", err)
	}
	m.ID = id
	return m
}

func emailSubjects(t *testing.T, st *store.Store) []string {
	t.Helper()
	emails, err := st.Emails(context.Background(), 100)
	if err != nil {
		t.Fatalf("list emails: %v", err)
	}
	out := make([]string, 0, len(emails))
	for _, e := range emails {
		out = append(out, e.Subject)
	}
	return out
}

// TestAlertLifecycle walks one incident from first missed check-in through the
// capped reminders to the all-clear.
func TestAlertLifecycle(t *testing.T) {
	ctx := context.Background()
	st := testStore(t)
	cfg := testConfig()
	c := New(st, cfg, testLogger())

	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	user := mustUser(t, st, "user@example.com", true)
	m := mustMonitor(t, st, user.ID, "tok", 5, base)

	// Never pinged: no due date, so nothing may happen.
	if err := c.RunAt(ctx, base.Add(time.Hour)); err != nil {
		t.Fatalf("run without ping: %v", err)
	}
	if got := emailSubjects(t, st); len(got) != 0 {
		t.Fatalf("never-pinged monitor sent mail: %v", got)
	}

	if _, err := st.RecordPing(ctx, m, "127.0.0.1", base); err != nil {
		t.Fatalf("record ping: %v", err)
	}
	loaded, err := st.MonitorByID(ctx, m.ID, user.ID)
	if err != nil {
		t.Fatalf("load monitor: %v", err)
	}
	if loaded.NextPingDueAt == nil || !loaded.NextPingDueAt.Equal(base.Add(5*time.Minute)) {
		t.Fatalf("unexpected due time: %v", loaded.NextPingDueAt)
	}

	// First missed check-in: state flips and one alert is queued.
	if err := c.RunAt(ctx, base.Add(10*time.Minute)); err != nil {
		t.Fatalf("first evaluation: %v", err)
	}
	loaded, _ = st.MonitorByID(ctx, m.ID, user.ID)
	if !loaded.IsOverdue || loaded.AlertEmailCount != 1 {
		t.Fatalf("expected alert state, got overdue=%v count=%d", loaded.IsOverdue, loaded.AlertEmailCount)
	}
	if got := emailSubjects(t, st); len(got) != 1 || got[0] != "Alert for backup" {
		t.Fatalf("expected one alert mail, got %v", got)
	}

	// A minute later: no reminder yet.
	if err := c.RunAt(ctx, base.Add(11*time.Minute)); err != nil {
		t.Fatalf("second evaluation: %v", err)
	}
	if got := emailSubjects(t, st); len(got) != 1 {
		t.Fatalf("reminder sent too early: %v", got)
	}

	// Daily reminders until the cap of 5 total mails.
	for day := 1; day <= 10; day++ {
		if err := c.RunAt(ctx, base.Add(10*time.Minute+time.Duration(day)*24*time.Hour)); err != nil {
			t.Fatalf("day %d evaluation: %v", day, err)
		}
	}
	if got := emailSubjects(t, st); len(got) != 5 {
		t.Fatalf("expected 5 alert mails, got %d: %v", len(got), got)
	}
	loaded, _ = st.MonitorByID(ctx, m.ID, user.ID)
	if loaded.AlertEmailCount != 5 {
		t.Fatalf("expected count 5, got %d", loaded.AlertEmailCount)
	}

	// A ping far later ends the incident with an all-clear.
	recovered := base.Add(11 * 24 * time.Hour)
	if _, err := st.RecordPing(ctx, loaded, "127.0.0.1", recovered); err != nil {
		t.Fatalf("recovery ping: %v", err)
	}
	if err := c.RunAt(ctx, recovered.Add(time.Minute)); err != nil {
		t.Fatalf("all-clear evaluation: %v", err)
	}
	loaded, _ = st.MonitorByID(ctx, m.ID, user.ID)
	if loaded.IsOverdue || loaded.AlertEmailCount != 0 || loaded.LastAlertEmailAt != nil {
		t.Fatalf("alert state not cleared: %+v", loaded)
	}
	got := emailSubjects(t, st)
	if len(got) != 6 || got[0] != "All-clear for backup" {
		t.Fatalf("expected all-clear mail, got %d: %v", len(got), got)
	}
}

// TestUnconfirmedUserGetsNoMail verifies that the alert state still flips when
// the address is unconfirmed, but no mail is queued.
func TestUnconfirmedUserGetsNoMail(t *testing.T) {
	ctx := context.Background()
	st := testStore(t)
	cfg := testConfig()
	c := New(st, cfg, testLogger())

	base := time.Date(2026, 2, 1, 8, 0, 0, 0, time.UTC)
	user := mustUser(t, st, "unconfirmed@example.com", false)
	m := mustMonitor(t, st, user.ID, "tok-unconfirmed", 5, base)
	if _, err := st.RecordPing(ctx, m, "127.0.0.1", base); err != nil {
		t.Fatalf("record ping: %v", err)
	}
	if err := c.RunAt(ctx, base.Add(10*time.Minute)); err != nil {
		t.Fatalf("evaluation: %v", err)
	}
	loaded, _ := st.MonitorByID(ctx, m.ID, user.ID)
	if !loaded.IsOverdue {
		t.Fatal("state should flip even without a confirmed address")
	}
	if got := emailSubjects(t, st); len(got) != 0 {
		t.Fatalf("unconfirmed user received mail: %v", got)
	}
}

// TestDisabledMonitorIsSkipped verifies that disabling silences evaluation.
func TestDisabledMonitorIsSkipped(t *testing.T) {
	ctx := context.Background()
	st := testStore(t)
	cfg := testConfig()
	c := New(st, cfg, testLogger())

	base := time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)
	user := mustUser(t, st, "disabled@example.com", true)
	m := mustMonitor(t, st, user.ID, "tok-disabled", 5, base)
	if _, err := st.RecordPing(ctx, m, "127.0.0.1", base); err != nil {
		t.Fatalf("record ping: %v", err)
	}
	if _, err := st.ToggleMonitor(ctx, m.ID, user.ID, base); err != nil {
		t.Fatalf("disable monitor: %v", err)
	}
	if err := c.RunAt(ctx, base.Add(time.Hour)); err != nil {
		t.Fatalf("evaluation: %v", err)
	}
	if got := emailSubjects(t, st); len(got) != 0 {
		t.Fatalf("disabled monitor sent mail: %v", got)
	}
}

// TestReEnableRecomputesDue ensures a monitor that is enabled again gets a due
// time from its last ping instead of staying silent until the next ping.
func TestReEnableRecomputesDue(t *testing.T) {
	ctx := context.Background()
	st := testStore(t)
	base := time.Date(2026, 4, 1, 8, 0, 0, 0, time.UTC)
	user := mustUser(t, st, "reenable@example.com", true)
	m := mustMonitor(t, st, user.ID, "tok-reenable", 30, base)
	if _, err := st.RecordPing(ctx, m, "127.0.0.1", base); err != nil {
		t.Fatalf("record ping: %v", err)
	}
	if _, err := st.ToggleMonitor(ctx, m.ID, user.ID, base); err != nil {
		t.Fatalf("disable: %v", err)
	}
	loaded, _ := st.MonitorByID(ctx, m.ID, user.ID)
	if loaded.NextPingDueAt != nil {
		t.Fatalf("disabled monitor still has a due time: %v", loaded.NextPingDueAt)
	}
	if _, err := st.ToggleMonitor(ctx, m.ID, user.ID, base.Add(time.Hour)); err != nil {
		t.Fatalf("enable: %v", err)
	}
	loaded, _ = st.MonitorByID(ctx, m.ID, user.ID)
	want := base.Add(30 * time.Minute)
	if loaded.NextPingDueAt == nil || !loaded.NextPingDueAt.Equal(want) {
		t.Fatalf("due time not recomputed: got %v want %v", loaded.NextPingDueAt, want)
	}
}
