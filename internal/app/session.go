package app

import (
	"context"
	"net/http"
	"time"

	"stillalive/internal/app/views"
	"stillalive/internal/auth"
	"stillalive/internal/store"
)

const sessionCookieName = "stillalive_session"

type contextKey int

const (
	sessionContextKey contextKey = iota
)

// sessionInfo is attached to every request. Sess is nil until a session is
// loaded or created, which keeps anonymous ping traffic cookie-free.
type sessionInfo struct {
	Sess *store.Session
	Raw  string
	User *store.User
}

func sessionFrom(ctx context.Context) *sessionInfo {
	info, _ := ctx.Value(sessionContextKey).(*sessionInfo)
	return info
}

// ensureSession loads or creates the server-side session for the request.
func (a *App) ensureSession(w http.ResponseWriter, r *http.Request) (*sessionInfo, error) {
	info := sessionFrom(r.Context())
	if info == nil {
		return nil, nil
	}
	if info.Sess != nil {
		return info, nil
	}
	raw, err := auth.Token(32)
	if err != nil {
		return nil, err
	}
	csrf, err := auth.Token(32)
	if err != nil {
		return nil, err
	}
	now := a.now()
	sess := &store.Session{
		IDHash:    auth.HashToken(raw),
		CSRFToken: csrf,
		CreatedAt: now,
		ExpiresAt: now.Add(a.cfg.SessionTTL),
	}
	if err := a.store.CreateSession(r.Context(), sess); err != nil {
		return nil, err
	}
	a.setSessionCookie(w, raw, a.cfg.SessionTTL)
	info.Sess = sess
	info.Raw = raw
	return info, nil
}

// rotateSession replaces the current session id with a fresh one, keeping the
// authenticated user. It is called after login and password changes to defeat
// session fixation.
func (a *App) rotateSession(w http.ResponseWriter, r *http.Request, userID int64, ttl time.Duration) error {
	info := sessionFrom(r.Context())
	if info == nil {
		return nil
	}
	raw, err := auth.Token(32)
	if err != nil {
		return err
	}
	csrf, err := auth.Token(32)
	if err != nil {
		return err
	}
	now := a.now()
	sess := &store.Session{
		IDHash:    auth.HashToken(raw),
		UserID:    &userID,
		CSRFToken: csrf,
		CreatedAt: now,
		ExpiresAt: now.Add(ttl),
	}
	if err := a.store.CreateSession(r.Context(), sess); err != nil {
		return err
	}
	if info.Sess != nil {
		if err := a.store.DeleteSession(r.Context(), info.Sess.IDHash); err != nil {
			a.logger.Warn("delete rotated session", "error", err)
		}
	}
	a.setSessionCookie(w, raw, ttl)
	info.Sess = sess
	info.Raw = raw
	info.User = nil
	return nil
}

func (a *App) setSessionCookie(w http.ResponseWriter, raw string, ttl time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    raw,
		Path:     "/",
		HttpOnly: true,
		Secure:   a.cfg.SessionSecure,
		SameSite: http.SameSiteLaxMode,
		Expires:  a.now().Add(ttl),
		MaxAge:   int(ttl / time.Second),
	})
}

func (a *App) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   a.cfg.SessionSecure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
		Expires:  time.Unix(1, 0),
	})
}

// currentUser returns the authenticated user, loading it once per request.
func (a *App) currentUser(r *http.Request) *store.User {
	info := sessionFrom(r.Context())
	if info == nil || info.Sess == nil || info.Sess.UserID == nil {
		return nil
	}
	if info.User != nil {
		return info.User
	}
	u, err := a.store.UserByID(r.Context(), *info.Sess.UserID)
	if err != nil {
		return nil
	}
	info.User = u
	return u
}

// pageData builds the view model shared by every page. It creates a session
// when necessary so that forms always have a CSRF token.
func (a *App) pageData(w http.ResponseWriter, r *http.Request) (views.Page, error) {
	info, err := a.ensureSession(w, r)
	if err != nil {
		return views.Page{}, err
	}
	p := views.Page{
		AppName:     a.cfg.AppName,
		AppURL:      a.cfg.AppURL,
		User:        a.currentUser(r),
		CurrentPath: r.URL.Path,
	}
	if info != nil && info.Sess != nil {
		p.CSRFToken = info.Sess.CSRFToken
		p.FlashSuccess = info.Sess.FlashSuccess
		p.FlashError = info.Sess.FlashError
	}
	return p, nil
}

// consumeFlash clears the one-shot messages after a page has been rendered.
func (a *App) consumeFlash(r *http.Request, p views.Page) {
	if p.FlashSuccess == "" && p.FlashError == "" {
		return
	}
	info := sessionFrom(r.Context())
	if info == nil || info.Sess == nil {
		return
	}
	if err := a.store.ClearSessionFlash(r.Context(), info.Sess.IDHash); err != nil {
		a.logger.Warn("clear flash", "error", err)
	}
}

// setFlash stores a one-shot message for the next rendered page.
func (a *App) setFlash(w http.ResponseWriter, r *http.Request, success, errMsg string) error {
	info, err := a.ensureSession(w, r)
	if err != nil {
		return err
	}
	if info == nil || info.Sess == nil {
		return nil
	}
	if err := a.store.SetSessionFlash(r.Context(), info.Sess.IDHash, success, errMsg); err != nil {
		return err
	}
	info.Sess.FlashSuccess = success
	info.Sess.FlashError = errMsg
	return nil
}
