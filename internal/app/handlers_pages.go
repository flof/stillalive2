package app

import (
	"net/http"

	"github.com/a-h/templ"

	"stillalive/internal/app/views"
)

// pageRenderer adapts a templ page component to an http handler.
func (a *App) pageRenderer(render func(views.Page) templ.Component) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, err := a.pageData(w, r)
		if err != nil {
			a.serverError(w, r, err)
			return
		}
		a.renderPage(w, r, http.StatusOK, render(p), p)
	}
}

func (a *App) home(w http.ResponseWriter, r *http.Request) {
	a.pageRenderer(views.HomePage)(w, r)
}

func (a *App) imprint(w http.ResponseWriter, r *http.Request) {
	a.pageRenderer(views.ImprintPage)(w, r)
}

func (a *App) terms(w http.ResponseWriter, r *http.Request) {
	a.pageRenderer(views.TermsPage)(w, r)
}

func (a *App) contact(w http.ResponseWriter, r *http.Request) {
	a.pageRenderer(views.ContactPage)(w, r)
}
