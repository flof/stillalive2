package app

import (
	"net/http"
	"strings"

	"stillalive/internal/app/views"
	"stillalive/internal/auth"
)

func (a *App) profile(w http.ResponseWriter, r *http.Request) {
	user := a.currentUser(r)
	p, err := a.pageData(w, r)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	loc := loadLocation(user.Timezone)
	created := formatShort(&user.CreatedAt, loc) + " (" + formatRelative(&user.CreatedAt, a.now()) + ")"
	a.renderPage(w, r, http.StatusOK, views.ProfilePage(p, user, created), p)
}

func (a *App) profileEditForm(w http.ResponseWriter, r *http.Request) {
	user := a.currentUser(r)
	p, err := a.pageData(w, r)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	values := views.ProfileValues{Name: user.Name, Timezone: user.Timezone}
	a.renderPage(w, r, http.StatusOK,
		views.ProfileEditPage(p, values, views.FormErrors{}, timezones), p)
}

func (a *App) profileEdit(w http.ResponseWriter, r *http.Request) {
	user := a.currentUser(r)
	values := views.ProfileValues{
		Name:     strings.TrimSpace(r.PostFormValue("name")),
		Timezone: strings.TrimSpace(r.PostFormValue("timezone")),
	}
	errs := views.FormErrors{}
	if values.Name == "" {
		errs["name"] = "Enter your name."
	} else if len(values.Name) > 255 {
		errs["name"] = "Your name must be at most 255 characters."
	}
	if !validTimezone(values.Timezone) {
		errs["timezone"] = "Choose a valid timezone."
	}
	if len(errs) > 0 {
		p, err := a.pageData(w, r)
		if err != nil {
			a.serverError(w, r, err)
			return
		}
		a.renderPage(w, r, http.StatusUnprocessableEntity,
			views.ProfileEditPage(p, values, errs, timezones), p)
		return
	}
	if err := a.store.UpdateProfile(r.Context(), user.ID, values.Name, values.Timezone); err != nil {
		a.serverError(w, r, err)
		return
	}
	a.flashAndRedirect(w, r, "Profile updated.", "", "/profile")
}

func (a *App) changePasswordForm(w http.ResponseWriter, r *http.Request) {
	p, err := a.pageData(w, r)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	a.renderPage(w, r, http.StatusOK, views.ChangePasswordPage(p, views.FormErrors{}), p)
}

func (a *App) changePassword(w http.ResponseWriter, r *http.Request) {
	user := a.currentUser(r)
	current := r.PostFormValue("current_password")
	newPassword := r.PostFormValue("new_password")
	confirmation := r.PostFormValue("new_password_confirmation")

	errs := views.FormErrors{}
	if !auth.CheckPassword(user.PasswordHash, current) {
		errs["current_password"] = "You entered the wrong password."
	}
	if len(newPassword) < 8 {
		errs["new_password"] = "Choose a password with at least 8 characters."
	} else if newPassword != confirmation {
		errs["new_password"] = "The passwords do not match."
	}
	if len(errs) > 0 {
		p, err := a.pageData(w, r)
		if err != nil {
			a.serverError(w, r, err)
			return
		}
		a.renderPage(w, r, http.StatusUnprocessableEntity,
			views.ChangePasswordPage(p, errs), p)
		return
	}

	hash, err := auth.HashPassword(newPassword)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	if err := a.store.UpdatePassword(r.Context(), user.ID, hash); err != nil {
		a.serverError(w, r, err)
		return
	}
	// Drop every other session and rotate the current one.
	if err := a.store.DeleteUserSessions(r.Context(), user.ID, ""); err != nil {
		a.logger.Warn("delete other sessions", "error", err)
	}
	if err := a.rotateSession(w, r, user.ID, a.cfg.SessionTTL); err != nil {
		a.serverError(w, r, err)
		return
	}
	a.flashAndRedirect(w, r, "Your password has been changed.", "", "/profile")
}

func (a *App) deleteAccountForm(w http.ResponseWriter, r *http.Request) {
	p, err := a.pageData(w, r)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	a.renderPage(w, r, http.StatusOK,
		views.DeleteAccountPage(p, a.currentUser(r), views.FormErrors{}), p)
}

func (a *App) deleteAccount(w http.ResponseWriter, r *http.Request) {
	user := a.currentUser(r)
	confirmation := strings.TrimSpace(r.PostFormValue("nameConfirm"))
	if confirmation != user.Name {
		p, err := a.pageData(w, r)
		if err != nil {
			a.serverError(w, r, err)
			return
		}
		a.renderPage(w, r, http.StatusUnprocessableEntity,
			views.DeleteAccountPage(p, user, views.FormErrors{
				"nameConfirm": "You entered the wrong name.",
			}), p)
		return
	}

	if err := a.store.DeleteUser(r.Context(), user.ID); err != nil {
		a.serverError(w, r, err)
		return
	}
	a.clearSessionCookie(w)
	a.redirect(w, r, "/")
}
