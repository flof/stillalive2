package views

import "stillalive/internal/store"

// Page is the data every page needs.
type Page struct {
	AppName      string
	AppURL       string
	User         *store.User
	CSRFToken    string
	FlashSuccess string
	FlashError   string
	CurrentPath  string
}

// FormErrors maps field names to validation messages.
type FormErrors map[string]string

// Get returns the message for a field, or an empty string.
func (e FormErrors) Get(key string) string { return e[key] }

// Any reports whether there are errors.
func (e FormErrors) Any() bool { return len(e) > 0 }

// LoginValues preserves the email input after a failed login.
type LoginValues struct {
	Email string
}

// RegisterValues preserves registration input after a validation error.
type RegisterValues struct {
	Name     string
	Email    string
	Timezone string
	Terms    bool
}

// MonitorValues preserves monitor form input.
type MonitorValues struct {
	Name    string
	Hours   string
	Minutes string
	Enabled bool
}

// ProfileValues preserves profile input.
type ProfileValues struct {
	Name     string
	Timezone string
}

// MonitorCard is the display model for a monitor.
type MonitorCard struct {
	ID                int64
	Name              string
	Enabled           bool
	IsAlerting        bool
	AwaitingFirstPing bool
	StatusLabel       string
	StatusClass       string
	Interval          string
	LastPing          string
	LastPingAbsolute  string
	Created           string
	PingURL           string
	PingCount         int64
	ShowURL           string
	EditURL           string
	ToggleURL         string
	DeleteURL         string
}
