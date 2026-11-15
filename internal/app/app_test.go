package app

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"stillalive/internal/auth"
	"stillalive/internal/config"
	"stillalive/internal/store"
)

var fixedNow = time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)

var csrfPattern = regexp.MustCompile(`name="csrf_token" value="([^"]+)"`)

func newTestApp(t *testing.T) (*App, *store.Store) {
	t.Helper()
	cfg := config.Load()
	cfg.AppName = "Still Alive"
	cfg.AppURL = "http://example.test"
	cfg.MinIntervalMinutes = 5
	cfg.MaxAlertEmails = 5
	cfg.PostPingURL = ""

	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	a, err := New(cfg, db, logger)
	if err != nil {
		t.Fatalf("new app: %v", err)
	}
	a.now = func() time.Time { return fixedNow }
	return a, store.New(db)
}

func newUser(t *testing.T, st *store.Store, email string) *store.User {
	t.Helper()
	confirmed := fixedNow
	u := &store.User{
		Name:             "User " + email,
		Email:            email,
		PasswordHash:     "x",
		Timezone:         "UTC",
		AcceptsTerms:     true,
		EmailConfirmedAt: &confirmed,
	}
	id, err := st.CreateUser(context.Background(), u)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	u.ID = id
	return u
}

func newMonitor(t *testing.T, st *store.Store, userID int64, token string, interval int) *store.Monitor {
	t.Helper()
	m := &store.Monitor{
		UserID:          userID,
		Name:            "nightly backup",
		MaxPingInterval: interval,
		Token:           token,
		Enabled:         true,
	}
	id, err := st.CreateMonitor(context.Background(), m, fixedNow)
	if err != nil {
		t.Fatalf("create monitor: %v", err)
	}
	m.ID = id
	return m
}

type client struct {
	t      *testing.T
	h      http.Handler
	cookie *http.Cookie
}

func newClient(t *testing.T, h http.Handler) *client { return &client{t: t, h: h} }

// loginAs creates a session row directly, bypassing the login form.
func (c *client) loginAs(st *store.Store, userID int64) {
	c.t.Helper()
	raw, err := auth.Token(32)
	if err != nil {
		c.t.Fatalf("token: %v", err)
	}
	sess := &store.Session{
		IDHash:    auth.HashToken(raw),
		UserID:    &userID,
		CSRFToken: "test-csrf-token",
		CreatedAt: fixedNow,
		ExpiresAt: fixedNow.Add(time.Hour),
	}
	if err := st.CreateSession(context.Background(), sess); err != nil {
		c.t.Fatalf("create session: %v", err)
	}
	c.cookie = &http.Cookie{Name: sessionCookieName, Value: raw, Path: "/"}
}

func (c *client) do(method, target string, form url.Values) *httptest.ResponseRecorder {
	c.t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req := httptest.NewRequest(method, target, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if c.cookie != nil {
		req.AddCookie(c.cookie)
	}
	rec := httptest.NewRecorder()
	c.h.ServeHTTP(rec, req)
	for _, ck := range rec.Result().Cookies() {
		if ck.Name != sessionCookieName {
			continue
		}
		if ck.MaxAge < 0 || ck.Value == "" {
			c.cookie = nil
			continue
		}
		copied := *ck
		c.cookie = &copied
	}
	return rec
}

func (c *client) csrf(body string) string {
	c.t.Helper()
	m := csrfPattern.FindStringSubmatch(body)
	if len(m) != 2 {
		c.t.Fatalf("no csrf token in response: %s", truncate(body, 400))
	}
	return m[1]
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// TestPingContract pins the machine interface: POST returns 201 OK, disabled
// monitors and unknown tokens return 404, and CSRF is not required.
func TestPingContract(t *testing.T) {
	a, st := newTestApp(t)
	c := newClient(t, a.Handler())
	user := newUser(t, st, "ping@example.com")
	m := newMonitor(t, st, user.ID, "ping-token", 30)

	rec := c.do(http.MethodPost, "/ping/ping-token", nil)
	if rec.Code != http.StatusCreated || strings.TrimSpace(rec.Body.String()) != "OK" {
		t.Fatalf("expected 201 OK, got %d %q", rec.Code, rec.Body.String())
	}
	if n, err := st.CountPings(context.Background(), m.ID); err != nil || n != 1 {
		t.Fatalf("expected one ping, got %d (%v)", n, err)
	}
	loaded, err := st.MonitorByID(context.Background(), m.ID, user.ID)
	if err != nil {
		t.Fatalf("load monitor: %v", err)
	}
	if loaded.LastPingAt == nil || !loaded.NextPingDueAt.Equal(fixedNow.Add(30*time.Minute)) {
		t.Fatalf("ping did not advance due time: %+v", loaded)
	}

	if _, err := st.ToggleMonitor(context.Background(), m.ID, user.ID, fixedNow); err != nil {
		t.Fatalf("disable monitor: %v", err)
	}
	rec = c.do(http.MethodPost, "/ping/ping-token", nil)
	if rec.Code != http.StatusNotFound || strings.TrimSpace(rec.Body.String()) != "Monitor exists but is not enabled" {
		t.Fatalf("unexpected disabled response: %d %q", rec.Code, rec.Body.String())
	}
	if n, _ := st.CountPings(context.Background(), m.ID); n != 1 {
		t.Fatalf("disabled ping must not be stored, count=%d", n)
	}

	rec = c.do(http.MethodPost, "/ping/does-not-exist", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown token: got %d", rec.Code)
	}

	rec = c.do(http.MethodGet, "/ping/ping-token", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "nightly backup") {
		t.Fatalf("ping form: %d", rec.Code)
	}
	rec = c.do(http.MethodGet, "/ping/does-not-exist", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown token form: got %d", rec.Code)
	}
}

// TestMonitorOwnership ensures one user cannot reach another user's monitor.
func TestMonitorOwnership(t *testing.T) {
	a, st := newTestApp(t)
	owner := newUser(t, st, "owner@example.com")
	intruder := newUser(t, st, "intruder@example.com")
	m := newMonitor(t, st, owner.ID, "secret-token", 30)

	c := newClient(t, a.Handler())
	c.loginAs(st, intruder.ID)

	for _, target := range []string{
		"/monitors/" + itoaTest(m.ID),
		"/monitors/" + itoaTest(m.ID) + "/edit",
		"/monitors/" + itoaTest(m.ID) + "/delete",
	} {
		if rec := c.do(http.MethodGet, target, nil); rec.Code != http.StatusNotFound {
			t.Fatalf("GET %s: expected 404, got %d", target, rec.Code)
		}
	}
	form := url.Values{"csrf_token": {"test-csrf-token"}}
	for _, target := range []string{
		"/monitors/" + itoaTest(m.ID) + "/toggle",
		"/monitors/" + itoaTest(m.ID) + "/edit",
		"/monitors/" + itoaTest(m.ID) + "/delete",
	} {
		if rec := c.do(http.MethodPost, target, form); rec.Code != http.StatusNotFound {
			t.Fatalf("POST %s: expected 404, got %d", target, rec.Code)
		}
	}

	// The list must not leak the foreign monitor.
	rec := c.do(http.MethodGet, "/monitors", nil)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "nightly backup") {
		t.Fatalf("foreign monitor leaked into list: %d", rec.Code)
	}
	if _, err := st.MonitorByID(context.Background(), m.ID, owner.ID); err != nil {
		t.Fatalf("owner monitor disappeared: %v", err)
	}
}

// TestCSRFRequired checks that unsafe requests without a token are rejected,
// while the ping endpoint stays exempt.
func TestCSRFRequired(t *testing.T) {
	a, st := newTestApp(t)
	user := newUser(t, st, "csrf@example.com")
	c := newClient(t, a.Handler())
	c.loginAs(st, user.ID)

	if rec := c.do(http.MethodPost, "/monitors", url.Values{"name": {"x"}}); rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 without CSRF, got %d", rec.Code)
	}
	if rec := c.do(http.MethodPost, "/ping/whatever", nil); rec.Code == http.StatusForbidden {
		t.Fatal("ping endpoint must not require CSRF")
	}
}

// TestRegistrationAndConfirmation walks the account lifecycle through HTTP.
func TestRegistrationAndConfirmation(t *testing.T) {
	a, st := newTestApp(t)
	c := newClient(t, a.Handler())

	rec := c.do(http.MethodGet, "/register", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("register form: %d", rec.Code)
	}
	form := url.Values{
		"csrf_token":            {c.csrf(rec.Body.String())},
		"name":                  {"Alice"},
		"email":                 {"alice@example.com"},
		"password":              {"secret123"},
		"password_confirmation": {"secret123"},
		"timezone":              {"Europe/Vienna"},
		"terms":                 {"1"},
	}
	rec = c.do(http.MethodPost, "/register", form)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/monitors" {
		t.Fatalf("register: %d location=%q", rec.Code, rec.Header().Get("Location"))
	}

	user, err := st.UserByEmail(context.Background(), "alice@example.com")
	if err != nil {
		t.Fatalf("user not created: %v", err)
	}
	if user.EmailConfirmedAt != nil || user.EmailConfirmToken == "" {
		t.Fatalf("unexpected new user state: %+v", user)
	}

	rec = c.do(http.MethodGet, "/monitors", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "not confirmed your email address") {
		t.Fatalf("expected unconfirmed banner, got %d", rec.Code)
	}

	rec = c.do(http.MethodGet, "/profile/confirm/"+itoaTest(user.ID)+"/"+user.EmailConfirmToken, nil)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("confirm: %d", rec.Code)
	}
	user, _ = st.UserByID(context.Background(), user.ID)
	if user.EmailConfirmedAt == nil {
		t.Fatal("email not confirmed")
	}

	rec = c.do(http.MethodGet, "/monitors", nil)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "not confirmed your email address") {
		t.Fatalf("banner still present after confirmation: %d", rec.Code)
	}
}

// TestMonitorCreateValidation checks interval bounds and error rendering.
func TestMonitorCreateValidation(t *testing.T) {
	a, st := newTestApp(t)
	user := newUser(t, st, "validation@example.com")
	c := newClient(t, a.Handler())
	c.loginAs(st, user.ID)

	form := url.Values{
		"csrf_token":                {"test-csrf-token"},
		"name":                      {"too short"},
		"max_ping_interval_hours":   {"0"},
		"max_ping_interval_minutes": {"2"},
		"enabled":                   {"1"},
	}
	rec := c.do(http.MethodPost, "/monitors", form)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Interval must be at least 5 minutes") {
		t.Fatalf("missing validation message: %s", truncate(rec.Body.String(), 500))
	}
}

func itoaTest(v int64) string { return strconv.FormatInt(v, 10) }

// TestStaticAssets guards the /static prefix handling: the embedded file
// system is rooted at static/, so the request path has to be stripped before
// lookup, and the assets must be served with the right content type.
func TestStaticAssets(t *testing.T) {
	a, _ := newTestApp(t)
	c := newClient(t, a.Handler())

	for _, tc := range []struct {
		path        string
		contentType string
	}{
		{"/static/app.css", "text/css"},
		{"/static/htmx.min.js", "javascript"},
	} {
		rec := c.do(http.MethodGet, tc.path, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s: expected 200, got %d", tc.path, rec.Code)
		}
		if body := rec.Body.Len(); body == 0 {
			t.Fatalf("GET %s: empty body", tc.path)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, tc.contentType) {
			t.Fatalf("GET %s: content type %q does not contain %q", tc.path, ct, tc.contentType)
		}
	}

	if rec := c.do(http.MethodGet, "/static/", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("GET /static/: expected 404, got %d", rec.Code)
	}
	if rec := c.do(http.MethodGet, "/static/missing.css", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("GET /static/missing.css: expected 404, got %d", rec.Code)
	}
}
