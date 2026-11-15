package app

import (
	"bytes"
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/a-h/templ"

	"stillalive/internal/app/views"
	"stillalive/internal/checker"
	"stillalive/internal/config"
	"stillalive/internal/mailer"
	"stillalive/internal/store"
)

//go:embed static/*
var staticFS embed.FS

// App holds the HTTP layer and its collaborators.
type App struct {
	cfg     config.Config
	db      *sql.DB
	store   *store.Store
	logger  *slog.Logger
	mailer  *mailer.Worker
	checker *checker.Checker
	static  http.Handler
	now     func() time.Time
}

// New wires the application together. The caller owns the database handle and
// is responsible for closing it.
func New(cfg config.Config, db *sql.DB, logger *slog.Logger) (*App, error) {
	assets, err := fs.Sub(staticFS, "static")
	if err != nil {
		return nil, fmt.Errorf("static assets: %w", err)
	}
	st := store.New(db)
	sender := mailer.NewSender(cfg, logger)

	a := &App{
		cfg:     cfg,
		db:      db,
		store:   st,
		logger:  logger,
		mailer:  mailer.NewWorker(st, sender, logger, 10*time.Second, 5),
		checker: checker.New(st, cfg, logger),
		// The file server is rooted at the embedded static/ subtree, so the
		// /static prefix has to be removed before lookup.
		static: http.StripPrefix("/static/", http.FileServer(http.FS(assets))),
		now:     func() time.Time { return time.Now().UTC() },
	}
	return a, nil
}

// Start launches the background workers. They stop when ctx is cancelled.
func (a *App) Start(ctx context.Context) {
	go a.checker.RunEvery(ctx, a.cfg.EvaluationInterval)
	go a.mailer.Run(ctx)
	go a.cleanupLoop(ctx)
}

func (a *App) cleanupLoop(ctx context.Context) {
	a.cleanup(ctx)
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.cleanup(ctx)
		}
	}
}

func (a *App) cleanup(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	now := a.now()
	if n, err := a.store.DeleteExpiredSessions(ctx, now); err != nil {
		a.logger.Warn("prune sessions", "error", err)
	} else if n > 0 {
		a.logger.Debug("pruned sessions", "count", n)
	}
	if n, err := a.store.DeleteExpiredPasswordResets(ctx, now); err != nil {
		a.logger.Warn("prune password resets", "error", err)
	} else if n > 0 {
		a.logger.Debug("pruned password resets", "count", n)
	}
	if a.cfg.PingRetention > 0 {
		if n, err := a.store.PrunePings(ctx, now.Add(-a.cfg.PingRetention)); err != nil {
			a.logger.Warn("prune pings", "error", err)
		} else if n > 0 {
			a.logger.Info("pruned pings", "count", n)
		}
	}
}

// routes registers every endpoint. Mutating monitor and profile endpoints are
// POST-only; the original PUT/PATCH/DELETE verbs are replaced by explicit
// action paths.
func (a *App) routes() http.Handler {
	mux := http.NewServeMux()

	mux.Handle("GET /static/", a.staticHandler())

	mux.HandleFunc("GET /{$}", a.home)
	mux.HandleFunc("GET /imprint", a.imprint)
	mux.HandleFunc("GET /terms", a.terms)
	mux.HandleFunc("GET /contact", a.contact)

	mux.HandleFunc("GET /login", a.guest(a.loginForm))
	mux.HandleFunc("POST /login", a.guest(a.login))
	mux.HandleFunc("POST /logout", a.auth(a.logout))
	mux.HandleFunc("GET /register", a.guest(a.registerForm))
	mux.HandleFunc("POST /register", a.guest(a.register))
	mux.HandleFunc("GET /password/forgot", a.guest(a.forgotPasswordForm))
	mux.HandleFunc("POST /password/forgot", a.guest(a.forgotPassword))
	mux.HandleFunc("GET /password/reset/{token}", a.guest(a.resetPasswordForm))
	mux.HandleFunc("POST /password/reset/{token}", a.guest(a.resetPassword))

	mux.HandleFunc("GET /monitors", a.auth(a.monitorList))
	mux.HandleFunc("GET /monitors/rows", a.auth(a.monitorRows))
	mux.HandleFunc("GET /monitors/new", a.auth(a.monitorNew))
	mux.HandleFunc("POST /monitors", a.auth(a.monitorCreate))
	mux.HandleFunc("GET /monitors/{id}", a.auth(a.monitorShow))
	mux.HandleFunc("GET /monitors/{id}/edit", a.auth(a.monitorEditForm))
	mux.HandleFunc("POST /monitors/{id}/edit", a.auth(a.monitorEdit))
	mux.HandleFunc("POST /monitors/{id}/toggle", a.auth(a.monitorToggle))
	mux.HandleFunc("GET /monitors/{id}/delete", a.auth(a.monitorDeleteForm))
	mux.HandleFunc("POST /monitors/{id}/delete", a.auth(a.monitorDelete))

	mux.HandleFunc("GET /ping/{token}", a.pingForm)
	mux.HandleFunc("POST /ping/{token}", a.ping)

	mux.HandleFunc("GET /profile", a.auth(a.profile))
	mux.HandleFunc("GET /profile/edit", a.auth(a.profileEditForm))
	mux.HandleFunc("POST /profile/edit", a.auth(a.profileEdit))
	mux.HandleFunc("GET /profile/password", a.auth(a.changePasswordForm))
	mux.HandleFunc("POST /profile/password", a.auth(a.changePassword))
	mux.HandleFunc("GET /profile/delete", a.auth(a.deleteAccountForm))
	mux.HandleFunc("POST /profile/delete", a.auth(a.deleteAccount))
	mux.HandleFunc("GET /profile/confirm/{id}/{token}", a.confirmEmail)
	mux.HandleFunc("POST /profile/confirm/resend", a.auth(a.resendConfirmation))

	mux.HandleFunc("/", a.notFound)

	return mux
}

func (a *App) staticHandler() http.Handler {
	cached := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Do not expose a directory listing of the embedded assets.
		if r.URL.Path == "/static/" {
			a.errorText(w, http.StatusNotFound, "Not found")
			return
		}
		if strings.HasSuffix(r.URL.Path, "htmx.min.js") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		a.static.ServeHTTP(w, r)
	})
	return cached
}

// renderPage writes a full page and clears one-shot flash messages only after
// the template rendered successfully.
func (a *App) renderPage(w http.ResponseWriter, r *http.Request, status int, c templ.Component, p views.Page) {
	if err := a.render(w, r, status, c); err != nil {
		return
	}
	a.consumeFlash(r, p)
}

func (a *App) render(w http.ResponseWriter, r *http.Request, status int, c templ.Component) error {
	var buf bytes.Buffer
	if err := c.Render(r.Context(), &buf); err != nil {
		a.logger.Error("render template", "path", r.URL.Path, "error", err)
		a.errorText(w, http.StatusInternalServerError, "Internal server error")
		return err
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if _, err := buf.WriteTo(w); err != nil {
		a.logger.Warn("write response", "path", r.URL.Path, "error", err)
	}
	return nil
}

// renderPartial writes an HTML fragment for htmx.
func (a *App) renderPartial(w http.ResponseWriter, r *http.Request, status int, c templ.Component) {
	_ = a.render(w, r, status, c)
}

func (a *App) errorText(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(message))
}

func (a *App) errorPage(w http.ResponseWriter, r *http.Request, status int, message string) {
	p, err := a.pageData(w, r)
	if err != nil {
		a.logger.Error("page data for error page", "error", err)
		a.errorText(w, status, message)
		return
	}
	_ = a.render(w, r, status, views.ErrorPage(p, status, message))
}

func (a *App) notFound(w http.ResponseWriter, r *http.Request) {
	a.errorPage(w, r, http.StatusNotFound, "The page you requested does not exist.")
}

func (a *App) serverError(w http.ResponseWriter, r *http.Request, err error) {
	a.logger.Error("request failed", "path", r.URL.Path, "error", err)
	a.errorPage(w, r, http.StatusInternalServerError,
		"Something went wrong on our side. The error has been logged.")
}

func (a *App) redirect(w http.ResponseWriter, r *http.Request, path string) {
	http.Redirect(w, r, path, http.StatusSeeOther)
}

func isHTMX(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true"
}

func (a *App) loadMonitor(w http.ResponseWriter, r *http.Request) (*store.Monitor, bool) {
	id, err := parseID(r.PathValue("id"))
	if err != nil {
		a.notFound(w, r)
		return nil, false
	}
	m, err := a.store.MonitorByID(r.Context(), id, a.currentUser(r).ID)
	if errors.Is(err, store.ErrNotFound) {
		a.notFound(w, r)
		return nil, false
	}
	if err != nil {
		a.serverError(w, r, err)
		return nil, false
	}
	return m, true
}
