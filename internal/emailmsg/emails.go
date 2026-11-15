package emailmsg

import (
	"fmt"
	"html"
	"net/url"
	"strings"
	"time"

	"stillalive/internal/config"
	"stillalive/internal/mailer"
	"stillalive/internal/store"
)

// Confirm builds the address-confirmation mail.
func Confirm(cfg config.Config, u *store.User) mailer.Message {
	link := fmt.Sprintf("%s/profile/confirm/%d/%s", cfg.AppURL, u.ID, url.PathEscape(u.EmailConfirmToken))
	text := fmt.Sprintf(`Hello %s,

please confirm your email address by opening this link:

%s

Thanks,
your team from %s
`, u.Name, link, cfg.AppName)
	body := fmt.Sprintf(`<p>Hello %s,</p>
<p>Please confirm your email address by opening this link:</p>
<p><a href="%s">%s</a></p>
<p>Thanks,<br>your team from %s</p>`,
		html.EscapeString(u.Name), html.EscapeString(link), html.EscapeString(link), html.EscapeString(cfg.AppName))
	return mailer.Message{
		To:      u.Email,
		Subject: "Confirm your email address",
		HTML:    layout(cfg, "Confirm your email address", body),
		Text:    text,
	}
}

// PasswordReset builds the password-reset mail.
func PasswordReset(cfg config.Config, u *store.User, token string) mailer.Message {
	link := fmt.Sprintf("%s/password/reset/%s?email=%s", cfg.AppURL,
		url.PathEscape(token), url.QueryEscape(u.Email))
	text := fmt.Sprintf(`Hello %s,

someone requested a password reset for your account. Open this link within one hour to choose a new password:

%s

If you did not request this, you can ignore this email.
`, u.Name, link)
	body := fmt.Sprintf(`<p>Hello %s,</p>
<p>Someone requested a password reset for your account. Open this link within one hour to choose a new password:</p>
<p><a href="%s">Reset your password</a></p>
<p>If you did not request this, you can ignore this email.</p>`,
		html.EscapeString(u.Name), html.EscapeString(link))
	return mailer.Message{
		To:      u.Email,
		Subject: "Reset your password",
		HTML:    layout(cfg, "Reset your password", body),
		Text:    text,
	}
}

// Alert builds the first or a reminder alert mail. alertNumber is the ordinal
// of this mail within the incident, starting at 1.
func Alert(cfg config.Config, c store.Candidate, alertNumber int) mailer.Message {
	monitorURL := fmt.Sprintf("%s/monitors/%d", cfg.AppURL, c.ID)
	lastPing := formatUTC(c.LastPingAt)

	footer := ""
	if remaining := cfg.MaxAlertEmails - alertNumber; remaining <= 0 {
		footer = "This was the last alert email. The monitor is now considered inactive until we receive a ping."
	} else {
		footer = fmt.Sprintf("You'll get up to %d more daily alert emails as long as we don't receive a ping. After that this monitor is considered inactive.", remaining)
	}

	text := fmt.Sprintf(`Alert for %s

We have NOT received a ping since %s.

Open the monitor: %s

%s

Thanks,
your team from %s
`, c.Name, lastPing, monitorURL, footer, cfg.AppName)
	body := fmt.Sprintf(`<p>We have <strong>not</strong> received a ping since %s.</p>
<p><a class="button" href="%s">Open monitor</a></p>
<p>%s</p>`,
		html.EscapeString(lastPing), html.EscapeString(monitorURL), html.EscapeString(footer))
	return mailer.Message{
		To:      c.UserEmail,
		Subject: "Alert for " + c.Name,
		HTML:    layout(cfg, "Alert for "+c.Name, body),
		Text:    text,
	}
}

// AllClear builds the recovery mail.
func AllClear(cfg config.Config, c store.Candidate) mailer.Message {
	monitorURL := fmt.Sprintf("%s/monitors/%d", cfg.AppURL, c.ID)
	lastPing := formatUTC(c.LastPingAt)
	text := fmt.Sprintf(`All-clear for %s

We finally received a ping at %s.

Open the monitor: %s

Everything seems good now. We send you an alert email if a future ping is overdue.

Thanks,
your team from %s
`, c.Name, lastPing, monitorURL, cfg.AppName)
	body := fmt.Sprintf(`<p>We finally received a ping at %s.</p>
<p><a class="button" href="%s">Open monitor</a></p>
<p>Everything seems good now. We send you an alert email if a future ping is overdue.</p>`,
		html.EscapeString(lastPing), html.EscapeString(monitorURL))
	return mailer.Message{
		To:      c.UserEmail,
		Subject: "All-clear for " + c.Name,
		HTML:    layout(cfg, "All-clear for "+c.Name, body),
		Text:    text,
	}
}

func layout(cfg config.Config, title, body string) string {
	body = strings.ReplaceAll(body, `class="button"`,
		`class="button" style="display:inline-block;padding:10px 16px;background:#1f6feb;color:#ffffff;border-radius:6px;text-decoration:none"`)
	return `<!DOCTYPE html>
<html>
<head><meta charset="utf-8"><title>` + html.EscapeString(title) + `</title></head>
<body style="margin:0;padding:24px;background:#f6f8fa;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;color:#1f2328">
<div style="max-width:560px;margin:0 auto;background:#ffffff;border:1px solid #d0d7de;border-radius:10px;padding:28px">
<div style="font-size:13px;font-weight:600;letter-spacing:.04em;text-transform:uppercase;color:#57606a;margin-bottom:12px">` + html.EscapeString(cfg.AppName) + `</div>
<h1 style="font-size:22px;margin:0 0 16px">` + html.EscapeString(title) + `</h1>
` + body + `
<hr style="border:none;border-top:1px solid #d0d7de;margin:24px 0 12px">
<p style="font-size:12px;color:#57606a;margin:0">Sent by ` + html.EscapeString(cfg.AppName) + `.</p>
</div>
</body>
</html>`
}

func formatUTC(t *time.Time) string {
	if t == nil {
		return "never"
	}
	return t.UTC().Format("2006-01-02 15:04:05")
}
