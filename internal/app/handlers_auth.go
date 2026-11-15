package app

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"stillalive/internal/app/views"
	"stillalive/internal/auth"
	"stillalive/internal/emailmsg"
	"stillalive/internal/store"
)

func (a *App) loginForm(w http.ResponseWriter, r *http.Request) {
	p, err := a.pageData(w, r)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	a.renderPage(w, r, http.StatusOK, views.LoginPage(p, views.LoginValues{}, views.FormErrors{}), p)
}

func (a *App) login(w http.ResponseWriter, r *http.Request) {
	email := strings.TrimSpace(r.PostFormValue("email"))
	password := r.PostFormValue("password")
	remember := formBool(r, "remember")

	generic := views.FormErrors{"email": "These credentials do not match our records."}
	user, err := a.store.UserByEmail(r.Context(), email)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		a.serverError(w, r, err)
		return
	}
	if user == nil || !auth.CheckPassword(user.PasswordHash, password) {
		p, perr := a.pageData(w, r)
		if perr != nil {
			a.serverError(w, r, perr)
			return
		}
		a.renderPage(w, r, http.StatusUnprocessableEntity,
			views.LoginPage(p, views.LoginValues{Email: email}, generic), p)
		return
	}

	ttl := a.cfg.SessionTTL
	message := "Welcome back."
	if remember {
		ttl = a.cfg.RememberTTL
	}
	if err := a.rotateSession(w, r, user.ID, ttl); err != nil {
		a.serverError(w, r, err)
		return
	}
	if err := a.setFlash(w, r, message, ""); err != nil {
		a.logger.Warn("set login flash", "error", err)
	}
	a.redirect(w, r, "/monitors")
}

func (a *App) logout(w http.ResponseWriter, r *http.Request) {
	info := sessionFrom(r.Context())
	if info != nil && info.Sess != nil {
		if err := a.store.DeleteSession(r.Context(), info.Sess.IDHash); err != nil {
			a.logger.Warn("delete session", "error", err)
		}
	}
	a.clearSessionCookie(w)
	a.redirect(w, r, "/")
}

func (a *App) registerForm(w http.ResponseWriter, r *http.Request) {
	p, err := a.pageData(w, r)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	values := views.RegisterValues{Timezone: "UTC"}
	a.renderPage(w, r, http.StatusOK,
		views.RegisterPage(p, values, views.FormErrors{}, timezones), p)
}

func (a *App) register(w http.ResponseWriter, r *http.Request) {
	values := views.RegisterValues{
		Name:     strings.TrimSpace(r.PostFormValue("name")),
		Email:    strings.TrimSpace(r.PostFormValue("email")),
		Timezone: strings.TrimSpace(r.PostFormValue("timezone")),
		Terms:    formBool(r, "terms"),
	}
	password := r.PostFormValue("password")
	confirmation := r.PostFormValue("password_confirmation")
	errs := views.FormErrors{}

	if values.Name == "" {
		errs["name"] = "Enter your name."
	} else if len(values.Name) > 255 {
		errs["name"] = "Your name must be at most 255 characters."
	}
	if values.Email == "" {
		errs["email"] = "Enter your email address."
	} else if !validEmail(values.Email) {
		errs["email"] = "Enter a valid email address."
	}
	if len(password) < 6 {
		errs["password"] = "Choose a password with at least 6 characters."
	} else if password != confirmation {
		errs["password"] = "The passwords do not match."
	}
	if !values.Terms {
		errs["terms"] = "You must accept the terms of service."
	}
	if !validTimezone(values.Timezone) {
		values.Timezone = "UTC"
		errs["timezone"] = "Choose a valid timezone."
	}

	if len(errs) > 0 {
		p, err := a.pageData(w, r)
		if err != nil {
			a.serverError(w, r, err)
			return
		}
		a.renderPage(w, r, http.StatusUnprocessableEntity,
			views.RegisterPage(p, values, errs, timezones), p)
		return
	}

	hash, err := auth.HashPassword(password)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	confirmToken, err := auth.Token(32)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	user := &store.User{
		Name:              values.Name,
		Email:             values.Email,
		PasswordHash:      hash,
		Timezone:          values.Timezone,
		AcceptsTerms:      true,
		EmailConfirmToken: confirmToken,
	}
	id, err := a.store.CreateUser(r.Context(), user)
	if errors.Is(err, store.ErrDuplicate) {
		errs["email"] = "An account with this email address already exists."
		p, perr := a.pageData(w, r)
		if perr != nil {
			a.serverError(w, r, perr)
			return
		}
		a.renderPage(w, r, http.StatusUnprocessableEntity,
			views.RegisterPage(p, values, errs, timezones), p)
		return
	}
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	user.ID = id
	a.enqueueConfirmation(r, user)

	if err := a.rotateSession(w, r, id, a.cfg.SessionTTL); err != nil {
		a.serverError(w, r, err)
		return
	}
	if err := a.setFlash(w, r,
		"Welcome! Check your inbox and confirm your email address so we can deliver alerts.", ""); err != nil {
		a.logger.Warn("set registration flash", "error", err)
	}
	a.redirect(w, r, "/monitors")
}

func (a *App) forgotPasswordForm(w http.ResponseWriter, r *http.Request) {
	p, err := a.pageData(w, r)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	a.renderPage(w, r, http.StatusOK, views.ForgotPasswordPage(p, views.FormErrors{}), p)
}

func (a *App) forgotPassword(w http.ResponseWriter, r *http.Request) {
	email := strings.TrimSpace(r.PostFormValue("email"))
	if !validEmail(email) {
		p, err := a.pageData(w, r)
		if err != nil {
			a.serverError(w, r, err)
			return
		}
		a.renderPage(w, r, http.StatusUnprocessableEntity,
			views.ForgotPasswordPage(p, views.FormErrors{"email": "Enter a valid email address."}), p)
		return
	}

	user, err := a.store.UserByEmail(r.Context(), email)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		a.serverError(w, r, err)
		return
	}
	if user != nil {
		token, err := auth.Token(32)
		if err != nil {
			a.serverError(w, r, err)
			return
		}
		now := a.now()
		if err := a.store.CreatePasswordReset(r.Context(), user.Email,
			auth.HashToken(token), now.Add(time.Hour), now); err != nil {
			a.serverError(w, r, err)
			return
		}
		msg := emailmsg.PasswordReset(a.cfg, user, token)
		if _, err := a.store.EnqueueEmail(r.Context(), msg.To, msg.Subject, msg.HTML, msg.Text, now); err != nil {
			a.logger.Error("queue password reset mail", "error", err)
		}
	}

	if err := a.setFlash(w, r,
		"If an account exists for that address, a reset link has been sent. Check your inbox.", ""); err != nil {
		a.logger.Warn("set reset flash", "error", err)
	}
	a.redirect(w, r, "/password/forgot")
}

func (a *App) resetPasswordForm(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	email := strings.TrimSpace(r.URL.Query().Get("email"))
	if !a.validReset(r, email, token) {
		a.errorPage(w, r, http.StatusBadRequest,
			"This password reset link is invalid or has expired. Request a new one.")
		return
	}
	p, err := a.pageData(w, r)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	a.renderPage(w, r, http.StatusOK,
		views.ResetPasswordPage(p, token, email, views.FormErrors{}), p)
}

func (a *App) resetPassword(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	email := strings.TrimSpace(r.PostFormValue("email"))
	password := r.PostFormValue("password")
	confirmation := r.PostFormValue("password_confirmation")

	if !a.validReset(r, email, token) {
		a.errorPage(w, r, http.StatusBadRequest,
			"This password reset link is invalid or has expired. Request a new one.")
		return
	}

	errors := views.FormErrors{}
	if len(password) < 8 {
		errors["password"] = "Choose a password with at least 8 characters."
	} else if password != confirmation {
		errors["password"] = "The passwords do not match."
	}
	if len(errors) > 0 {
		p, err := a.pageData(w, r)
		if err != nil {
			a.serverError(w, r, err)
			return
		}
		a.renderPage(w, r, http.StatusUnprocessableEntity,
			views.ResetPasswordPage(p, token, email, errors), p)
		return
	}

	ok, err := a.store.ConsumePasswordReset(r.Context(), email, auth.HashToken(token), a.now())
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	if !ok {
		a.errorPage(w, r, http.StatusBadRequest,
			"This password reset link is invalid or has expired. Request a new one.")
		return
	}

	user, err := a.store.UserByEmail(r.Context(), email)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	if err := a.store.UpdatePassword(r.Context(), user.ID, hash); err != nil {
		a.serverError(w, r, err)
		return
	}
	if err := a.store.DeleteUserSessions(r.Context(), user.ID, ""); err != nil {
		a.logger.Warn("delete sessions after password reset", "error", err)
	}
	a.clearSessionCookie(w)
	if err := a.setFlash(w, r, "Your password has been reset. Please log in.", ""); err != nil {
		a.logger.Warn("set password reset flash", "error", err)
	}
	a.redirect(w, r, "/login")
}

func (a *App) validReset(r *http.Request, email, token string) bool {
	if email == "" || token == "" {
		return false
	}
	ok, err := a.store.PasswordResetExists(r.Context(), email, auth.HashToken(token), a.now())
	if err != nil {
		a.logger.Warn("check reset token", "error", err)
		return false
	}
	return ok
}

// confirmEmail handles the link from the confirmation mail. It deliberately
// does not log the visitor in: the link is a bearer token that arrives by
// email and must not double as a session credential.
func (a *App) confirmEmail(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r.PathValue("id"))
	token := r.PathValue("token")
	if err != nil {
		a.flashAndRedirect(w, r, "", "Invalid confirmation link.", "/")
		return
	}
	user, err := a.store.UserByID(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		a.flashAndRedirect(w, r, "", "Invalid confirmation link.", "/")
		return
	}
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	if user.EmailConfirmedAt != nil {
		a.flashAndRedirect(w, r, "", "Your email address has already been confirmed.", "/")
		return
	}
	ok, err := a.store.ConfirmEmail(r.Context(), user.ID, token)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	if !ok {
		a.flashAndRedirect(w, r, "", "Invalid confirmation link.", "/")
		return
	}
	a.flashAndRedirect(w, r, "Your email address has been confirmed. Alerts can now reach you.", "", "/monitors")
}

func (a *App) resendConfirmation(w http.ResponseWriter, r *http.Request) {
	user := a.currentUser(r)
	if user == nil {
		a.redirect(w, r, "/login")
		return
	}
	if user.EmailConfirmedAt != nil {
		a.flashAndRedirect(w, r, "", "Your email address is already confirmed.", "/profile")
		return
	}
	token, err := auth.Token(32)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	if err := a.store.SetEmailConfirmToken(r.Context(), user.ID, token); err != nil {
		a.serverError(w, r, err)
		return
	}
	user.EmailConfirmToken = token
	a.enqueueConfirmation(r, user)

	if isHTMX(r) {
		p, err := a.pageData(w, r)
		if err != nil {
			a.serverError(w, r, err)
			return
		}
		a.renderPartial(w, r, http.StatusOK,
			views.ConfirmBanner(p, "Confirmation email sent. Check your inbox."))
		return
	}
	a.flashAndRedirect(w, r, "Confirmation email sent. Check your inbox.", "", "/profile")
}

func (a *App) enqueueConfirmation(r *http.Request, user *store.User) {
	msg := emailmsg.Confirm(a.cfg, user)
	if _, err := a.store.EnqueueEmail(r.Context(), msg.To, msg.Subject, msg.HTML, msg.Text, a.now()); err != nil {
		a.logger.Error("queue confirmation mail", "error", err)
	}
}

// flashAndRedirect stores a one-shot message and redirects. An empty success
// and error is equivalent to a plain redirect.
func (a *App) flashAndRedirect(w http.ResponseWriter, r *http.Request, success, errMsg, target string) {
	if success != "" || errMsg != "" {
		if err := a.setFlash(w, r, success, errMsg); err != nil {
			a.logger.Warn("set flash", "error", err)
		}
	}
	a.redirect(w, r, target)
}

func parseID(value string) (int64, error) {
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 {
		return 0, errors.New("invalid id")
	}
	return id, nil
}
