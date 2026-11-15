package app

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"stillalive/internal/app/views"
	"stillalive/internal/auth"
	"stillalive/internal/store"
)

func (a *App) monitorList(w http.ResponseWriter, r *http.Request) {
	monitors, err := a.store.MonitorsByUser(r.Context(), a.currentUser(r).ID)
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
		views.MonitorListPage(p, a.monitorCards(r, monitors)), p)
}

func (a *App) monitorRows(w http.ResponseWriter, r *http.Request) {
	monitors, err := a.store.MonitorsByUser(r.Context(), a.currentUser(r).ID)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	p, err := a.pageData(w, r)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	a.renderPartial(w, r, http.StatusOK, views.MonitorRows(p, a.monitorCards(r, monitors)))
}

func (a *App) monitorNew(w http.ResponseWriter, r *http.Request) {
	p, err := a.pageData(w, r)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	values := views.MonitorValues{
		Hours:   "0",
		Minutes: strconv.Itoa(a.cfg.MinIntervalMinutes),
		Enabled: true,
	}
	a.renderPage(w, r, http.StatusOK,
		views.MonitorNewPage(p, values, views.FormErrors{}), p)
}

func (a *App) monitorCreate(w http.ResponseWriter, r *http.Request) {
	values, interval, errs := a.monitorValuesFromForm(r)
	if len(errs) > 0 {
		a.renderMonitorFormErrors(w, r, values, errs, false, nil)
		return
	}

	user := a.currentUser(r)
	now := a.now()
	monitor := &store.Monitor{
		UserID:          user.ID,
		Name:            values.Name,
		MaxPingInterval: interval,
		Enabled:         values.Enabled,
	}
	var id int64
	var err error
	for attempt := 0; attempt < 5; attempt++ {
		monitor.Token, err = auth.Token(a.cfg.TokenLength)
		if err != nil {
			a.serverError(w, r, err)
			return
		}
		id, err = a.store.CreateMonitor(r.Context(), monitor, now)
		if !errors.Is(err, store.ErrDuplicate) {
			break
		}
	}
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	a.flashAndRedirect(w, r, "Monitor created.", "", "/monitors/"+strconv.FormatInt(id, 10))
}

func (a *App) monitorShow(w http.ResponseWriter, r *http.Request) {
	m, ok := a.loadMonitor(w, r)
	if !ok {
		return
	}
	card := a.monitorCard(r, m)
	count, err := a.store.CountPings(r.Context(), m.ID)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	card.PingCount = count
	p, err := a.pageData(w, r)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	a.renderPage(w, r, http.StatusOK, views.MonitorShowPage(p, card), p)
}

func (a *App) monitorEditForm(w http.ResponseWriter, r *http.Request) {
	m, ok := a.loadMonitor(w, r)
	if !ok {
		return
	}
	p, err := a.pageData(w, r)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	card := a.monitorCard(r, m)
	values := views.MonitorValues{
		Name:    m.Name,
		Hours:   strconv.Itoa(m.MaxPingInterval / 60),
		Minutes: strconv.Itoa(m.MaxPingInterval % 60),
		Enabled: m.Enabled,
	}
	a.renderPage(w, r, http.StatusOK,
		views.MonitorEditPage(p, card, values, views.FormErrors{}), p)
}

func (a *App) monitorEdit(w http.ResponseWriter, r *http.Request) {
	m, ok := a.loadMonitor(w, r)
	if !ok {
		return
	}
	values, interval, errs := a.monitorValuesFromForm(r)
	if len(errs) > 0 {
		a.renderMonitorFormErrors(w, r, values, errs, true, m)
		return
	}
	if err := a.store.UpdateMonitorSettings(r.Context(), m, values.Name, interval, values.Enabled, a.now()); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			a.notFound(w, r)
			return
		}
		a.serverError(w, r, err)
		return
	}
	a.flashAndRedirect(w, r, "Monitor updated.", "", "/monitors/"+strconv.FormatInt(m.ID, 10))
}

func (a *App) monitorToggle(w http.ResponseWriter, r *http.Request) {
	m, ok := a.loadMonitor(w, r)
	if !ok {
		return
	}
	enabled, err := a.store.ToggleMonitor(r.Context(), m.ID, m.UserID, a.now())
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			a.notFound(w, r)
			return
		}
		a.serverError(w, r, err)
		return
	}
	fresh, err := a.store.MonitorByID(r.Context(), m.ID, m.UserID)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	if isHTMX(r) {
		p, err := a.pageData(w, r)
		if err != nil {
			a.serverError(w, r, err)
			return
		}
		a.renderPartial(w, r, http.StatusOK, views.MonitorRow(p, a.monitorCard(r, fresh)))
		return
	}
	message := "Monitor disabled."
	if enabled {
		message = "Monitor enabled."
	}
	a.flashAndRedirect(w, r, message, "", "/monitors")
}

func (a *App) monitorDeleteForm(w http.ResponseWriter, r *http.Request) {
	m, ok := a.loadMonitor(w, r)
	if !ok {
		return
	}
	card := a.monitorCard(r, m)
	p, err := a.pageData(w, r)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	if isHTMX(r) {
		a.renderPartial(w, r, http.StatusOK, views.MonitorDeleteBox(p, card))
		return
	}
	a.renderPage(w, r, http.StatusOK, views.MonitorDeletePage(p, card), p)
}

func (a *App) monitorDelete(w http.ResponseWriter, r *http.Request) {
	m, ok := a.loadMonitor(w, r)
	if !ok {
		return
	}
	if err := a.store.DeleteMonitor(r.Context(), m.ID, m.UserID); err != nil && !errors.Is(err, store.ErrNotFound) {
		a.serverError(w, r, err)
		return
	}
	a.flashAndRedirect(w, r, "Monitor deleted.", "", "/monitors")
}

// monitorValuesFromForm reads and validates the monitor form.
func (a *App) monitorValuesFromForm(r *http.Request) (views.MonitorValues, int, views.FormErrors) {
	values := views.MonitorValues{
		Name:    strings.TrimSpace(r.PostFormValue("name")),
		Hours:   strings.TrimSpace(r.PostFormValue("max_ping_interval_hours")),
		Minutes: strings.TrimSpace(r.PostFormValue("max_ping_interval_minutes")),
		Enabled: formBool(r, "enabled"),
	}
	errs := views.FormErrors{}
	if values.Name == "" {
		errs["name"] = "Enter a name for the monitor."
	} else if len(values.Name) > 255 {
		errs["name"] = "The name must be at most 255 characters."
	}
	interval, msg := a.parseInterval(r)
	if msg != "" {
		errs["max_ping_interval"] = msg
	}
	return values, interval, errs
}

func (a *App) renderMonitorFormErrors(w http.ResponseWriter, r *http.Request,
	values views.MonitorValues, errs views.FormErrors, edit bool, m *store.Monitor) {
	p, err := a.pageData(w, r)
	if err != nil {
		a.serverError(w, r, err)
		return
	}
	if edit && m != nil {
		a.renderPage(w, r, http.StatusUnprocessableEntity,
			views.MonitorEditPage(p, a.monitorCard(r, m), values, errs), p)
		return
	}
	a.renderPage(w, r, http.StatusUnprocessableEntity,
		views.MonitorNewPage(p, values, errs), p)
}

// monitorCards translates domain monitors into display models.
func (a *App) monitorCards(r *http.Request, monitors []store.Monitor) []views.MonitorCard {
	out := make([]views.MonitorCard, 0, len(monitors))
	for i := range monitors {
		out = append(out, a.monitorCard(r, &monitors[i]))
	}
	return out
}

func (a *App) monitorCard(r *http.Request, m *store.Monitor) views.MonitorCard {
	now := a.now()
	loc := time.UTC
	if user := a.currentUser(r); user != nil {
		loc = loadLocation(user.Timezone)
	}
	card := views.MonitorCard{
		ID:                m.ID,
		Name:              m.Name,
		Enabled:           m.Enabled,
		IsAlerting:        m.IsOverdue,
		AwaitingFirstPing: m.Enabled && m.LastPingAt == nil,
		Interval:          formatInterval(m.MaxPingInterval),
		LastPing:          formatRelative(m.LastPingAt, now),
		LastPingAbsolute:  formatAbsolute(m.LastPingAt, loc),
		Created:           formatShort(&m.CreatedAt, loc) + " (" + formatRelative(&m.CreatedAt, now) + ")",
		PingURL:           a.cfg.AppURL + "/ping/" + m.Token,
		ShowURL:           "/monitors/" + strconv.FormatInt(m.ID, 10),
		EditURL:           "/monitors/" + strconv.FormatInt(m.ID, 10) + "/edit",
		ToggleURL:         "/monitors/" + strconv.FormatInt(m.ID, 10) + "/toggle",
		DeleteURL:         "/monitors/" + strconv.FormatInt(m.ID, 10) + "/delete",
	}
	switch {
	case !m.Enabled:
		card.StatusLabel, card.StatusClass = "Disabled", "muted"
	case m.LastPingAt == nil:
		card.StatusLabel, card.StatusClass = "Waiting for first ping", "warn"
	case m.Overdue(now):
		card.StatusLabel, card.StatusClass = "Overdue", "danger"
	default:
		card.StatusLabel, card.StatusClass = "Healthy", "ok"
	}
	return card
}
