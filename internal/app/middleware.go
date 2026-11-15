package app

import (
	"context"
	"crypto/subtle"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"stillalive/internal/auth"
	"stillalive/internal/store"
)

// Handler returns the complete HTTP handler with all middleware applied.
func (a *App) Handler() http.Handler {
	return a.recoverer(a.logRequests(a.securityHeaders(a.loadSession(a.checkCSRF(a.routes())))))
}

// loadSession attaches the session (if a valid cookie is present) to the
// request. It never creates sessions; page rendering does that on demand.
func (a *App) loadSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		info := &sessionInfo{}
		ctx := context.WithValue(r.Context(), sessionContextKey, info)
		r = r.WithContext(ctx)

		cookie, err := r.Cookie(sessionCookieName)
		if err == nil && cookie.Value != "" {
			sess, err := a.store.SessionByIDHash(ctx, auth.HashToken(cookie.Value), a.now())
			switch {
			case err == nil:
				info.Sess = sess
				info.Raw = cookie.Value
			case err == store.ErrNotFound:
				a.clearSessionCookie(w)
			default:
				a.logger.Error("load session", "error", err)
			}
		}
		next.ServeHTTP(w, r)
	})
}

// checkCSRF rejects unsafe requests that lack a matching token. The ping
// endpoint is exempt because scripts cannot obtain a token.
func (a *App) checkCSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/ping/") {
			next.ServeHTTP(w, r)
			return
		}
		if err := r.ParseForm(); err != nil {
			a.errorText(w, http.StatusBadRequest, "Invalid form submission")
			return
		}
		info := sessionFrom(r.Context())
		if info == nil || info.Sess == nil {
			a.errorText(w, http.StatusForbidden, "Your session has expired. Reload the page and try again.")
			return
		}
		sent := r.PostFormValue("csrf_token")
		if subtle.ConstantTimeCompare([]byte(sent), []byte(info.Sess.CSRFToken)) != 1 {
			a.errorText(w, http.StatusForbidden, "Invalid security token. Reload the page and try again.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// securityHeaders sets conservative defaults that work without inline scripts
// or third-party assets.
func (a *App) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'self'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		next.ServeHTTP(w, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	return s.ResponseWriter.Write(b)
}

func (a *App) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := a.now()
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		status := rec.status
		if status == 0 {
			status = http.StatusOK
		}
		a.logger.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", status,
			"duration", time.Since(start).Round(time.Millisecond),
			"ip", a.clientIP(r),
		)
	})
}

func (a *App) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				if v == http.ErrAbortHandler {
					panic(v)
				}
				a.logger.Error("panic", "path", r.URL.Path, "panic", v)
				a.errorPage(w, r, http.StatusInternalServerError,
					"Something went wrong on our side. The error has been logged.")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// auth wraps handlers that require a logged-in user.
func (a *App) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if a.currentUser(r) == nil {
			info := sessionFrom(r.Context())
			if info != nil && info.Sess != nil && info.Sess.UserID != nil {
				// The session references a user that no longer exists.
				_ = a.store.DeleteSession(r.Context(), info.Sess.IDHash)
				a.clearSessionCookie(w)
			}
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next(w, r)
	}
}

// guest wraps handlers that are only useful to anonymous visitors.
func (a *App) guest(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if a.currentUser(r) != nil {
			http.Redirect(w, r, "/monitors", http.StatusSeeOther)
			return
		}
		next(w, r)
	}
}

// clientIP honours X-Forwarded-For only when the deployment explicitly trusts
// a proxy in front of the application.
func (a *App) clientIP(r *http.Request) string {
	if a.cfg.TrustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			if i := strings.IndexByte(xff, ','); i >= 0 {
				return strings.TrimSpace(xff[:i])
			}
			return strings.TrimSpace(xff)
		}
		if ip := r.Header.Get("X-Real-IP"); ip != "" {
			return strings.TrimSpace(ip)
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (a *App) loggerWith(r *http.Request) *slog.Logger {
	return a.logger.With("path", r.URL.Path)
}
