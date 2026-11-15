package app

import (
	"errors"
	"net/http"

	"stillalive/internal/app/views"
	"stillalive/internal/store"
)

func (a *App) pingForm(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	monitor, err := a.store.MonitorByToken(r.Context(), token)
	if errors.Is(err, store.ErrNotFound) {
		a.errorPage(w, r, http.StatusNotFound, "This ping URL does not exist.")
		return
	}
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	p, err := a.pageData(w, r)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	a.renderPage(w, r, http.StatusOK,
		views.PingPage(p, token, monitor.Name, monitor.Enabled), p)
}

func (a *App) ping(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	token := r.PathValue("token")
	fromForm := r.PostFormValue("from_form") == "1"

	monitor, err := a.store.MonitorByToken(r.Context(), token)
	if errors.Is(err, store.ErrNotFound) {
		if fromForm {
			a.flashAndRedirect(w, r, "", "This ping URL does not exist.", "/")
			return
		}
		a.errorText(w, http.StatusNotFound, "Monitor not found")
		return
	}
	if err != nil {
		a.serverError(w, r, err)
		return
	}

	if !monitor.Enabled {
		if fromForm {
			a.flashAndRedirect(w, r, "", "Monitor is not enabled.", "/ping/"+token)
			return
		}
		a.errorText(w, http.StatusNotFound, "Monitor exists but is not enabled")
		return
	}

	now := a.now()
	if _, err := a.store.RecordPing(r.Context(), monitor, a.clientIP(r), now); err != nil {
		a.serverError(w, r, err)
		return
	}

	if fromForm {
		stamp := now.In(loadLocation(a.userTimezone(r))).Format("2006-01-02 15:04:05 MST")
		a.flashAndRedirect(w, r, "Ping successfully saved at "+stamp, "", "/ping/"+token)
		return
	}
	a.errorText(w, http.StatusCreated, "OK")
}

func (a *App) userTimezone(r *http.Request) string {
	if user := a.currentUser(r); user != nil {
		return user.Timezone
	}
	return "UTC"
}
