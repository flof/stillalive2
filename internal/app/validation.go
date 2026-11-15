package app

import (
	"fmt"
	"net/http"
	"net/mail"
	"strconv"
	"strings"
	"time"
)

// timezones is the list offered in the registration and profile forms. It is a
// curated subset of the IANA database; any other valid zone name submitted by
// a client is still accepted after LoadLocation succeeds.
var timezones = []string{
	"UTC",
	"Europe/Vienna",
	"Europe/Berlin",
	"Europe/Zurich",
	"Europe/London",
	"Europe/Paris",
	"Europe/Madrid",
	"Europe/Rome",
	"Europe/Amsterdam",
	"Europe/Brussels",
	"Europe/Copenhagen",
	"Europe/Stockholm",
	"Europe/Oslo",
	"Europe/Helsinki",
	"Europe/Warsaw",
	"Europe/Prague",
	"Europe/Budapest",
	"Europe/Bucharest",
	"Europe/Athens",
	"Europe/Moscow",
	"Europe/Lisbon",
	"Europe/Dublin",
	"Europe/Istanbul",
	"America/New_York",
	"America/Chicago",
	"America/Denver",
	"America/Los_Angeles",
	"America/Anchorage",
	"America/Toronto",
	"America/Mexico_City",
	"America/Bogota",
	"America/Sao_Paulo",
	"America/Argentina/Buenos_Aires",
	"Africa/Cairo",
	"Africa/Johannesburg",
	"Africa/Lagos",
	"Africa/Nairobi",
	"Asia/Jerusalem",
	"Asia/Dubai",
	"Asia/Karachi",
	"Asia/Kolkata",
	"Asia/Bangkok",
	"Asia/Singapore",
	"Asia/Hong_Kong",
	"Asia/Shanghai",
	"Asia/Tokyo",
	"Asia/Seoul",
	"Australia/Perth",
	"Australia/Adelaide",
	"Australia/Brisbane",
	"Australia/Sydney",
	"Pacific/Auckland",
}

// loadLocation resolves a user timezone, falling back to UTC for empty or
// unknown values so display code can never fail.
func loadLocation(name string) *time.Location {
	if name == "" {
		return time.UTC
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return time.UTC
	}
	return loc
}

// validTimezone reports whether name is a known IANA zone.
func validTimezone(name string) bool {
	if name == "" {
		return false
	}
	_, err := time.LoadLocation(name)
	return err == nil
}

// validEmail checks the address with net/mail and rejects display-name forms.
func validEmail(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 255 {
		return false
	}
	addr, err := mail.ParseAddress(value)
	if err != nil {
		return false
	}
	return addr.Address == value && strings.Contains(value, "@")
}

// parseInterval adds the hours and minutes fields of a monitor form and
// enforces the configured minimum. It returns the total in minutes.
func (a *App) parseInterval(r *http.Request) (int, string) {
	hours, errH := strconv.Atoi(strings.TrimSpace(r.PostFormValue("max_ping_interval_hours")))
	minutes, errM := strconv.Atoi(strings.TrimSpace(r.PostFormValue("max_ping_interval_minutes")))
	if errH != nil || errM != nil {
		return 0, "Enter a whole number of hours and minutes."
	}
	if hours < 0 || hours > 200 || minutes < 0 || minutes > 2000 {
		return 0, "Hours must be between 0 and 200, minutes between 0 and 2000."
	}
	total := hours*60 + minutes
	if total < a.cfg.MinIntervalMinutes {
		return 0, fmt.Sprintf("Interval must be at least %d minutes.", a.cfg.MinIntervalMinutes)
	}
	if total > 200*60+2000 {
		return 0, "Interval is too large."
	}
	return total, ""
}

// formBool interprets a checkbox that may be accompanied by a hidden "0"
// field. Any truthy submitted value wins.
func formBool(r *http.Request, key string) bool {
	if err := r.ParseForm(); err != nil {
		return false
	}
	for _, v := range r.PostForm[key] {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "1", "true", "on", "yes":
			return true
		}
	}
	return false
}

// formatInterval renders a minute count as "1 h 30 min", "2 h" or "5 min".
func formatInterval(minutes int) string {
	h := minutes / 60
	m := minutes % 60
	switch {
	case h > 0 && m > 0:
		return fmt.Sprintf("%d h %d min", h, m)
	case h > 0:
		return fmt.Sprintf("%d h", h)
	default:
		return fmt.Sprintf("%d min", m)
	}
}

// formatAbsolute renders a timestamp in the user's zone.
func formatAbsolute(t *time.Time, loc *time.Location) string {
	if t == nil {
		return "Never"
	}
	return t.In(loc).Format("2006-01-02 15:04:05")
}

// formatShort renders a compact timestamp in the user's zone.
func formatShort(t *time.Time, loc *time.Location) string {
	if t == nil {
		return "Never"
	}
	return t.In(loc).Format("2006-01-02 15:04")
}

// formatRelative renders how long ago t was.
func formatRelative(t *time.Time, now time.Time) string {
	if t == nil {
		return "Never"
	}
	d := now.Sub(*t)
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return plural(int(d/time.Minute), "minute") + " ago"
	case d < 24*time.Hour:
		return plural(int(d/time.Hour), "hour") + " ago"
	default:
		return plural(int(d/(24*time.Hour)), "day") + " ago"
	}
}

func plural(n int, unit string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", unit)
	}
	return fmt.Sprintf("%d %ss", n, unit)
}
