package mailer

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"stillalive/internal/config"
)

// Message is one outbound email.
type Message struct {
	To      string
	Subject string
	HTML    string
	Text    string
}

// Sender delivers a message.
type Sender interface {
	Send(ctx context.Context, m Message) error
}

// NewSender returns an SMTP sender when a host is configured, otherwise a
// sender that writes messages to the log.
func NewSender(cfg config.Config, logger *slog.Logger) Sender {
	if cfg.LogMail() {
		return &LogSender{logger: logger}
	}
	return &SMTPSender{cfg: cfg.SMTP}
}

// LogSender is the development/offline mailer.
type LogSender struct {
	logger *slog.Logger
}

// Send logs the message instead of delivering it.
func (l *LogSender) Send(_ context.Context, m Message) error {
	l.logger.Info("mail (log mode)",
		"to", m.To,
		"subject", m.Subject,
		"text", m.Text,
	)
	return nil
}

// SMTPSender delivers mail over SMTP with STARTTLS or implicit TLS.
type SMTPSender struct {
	cfg config.SMTP
}

// Send delivers one message.
func (s *SMTPSender) Send(ctx context.Context, m Message) error {
	addr := net.JoinHostPort(s.cfg.Host, strconv.Itoa(s.cfg.Port))
	dialer := &net.Dialer{Timeout: 15 * time.Second}

	implicitTLS := false
	var (
		conn net.Conn
		err  error
	)
	switch strings.ToLower(s.cfg.Encryption) {
	case "tls", "ssl", "implicit":
		implicitTLS = true
		conn, err = tls.DialWithDialer(dialer, "tcp", addr, &tls.Config{
			ServerName: s.cfg.Host,
			MinVersion: tls.VersionTLS12,
		})
	default:
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("smtp dial %s: %w", addr, err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))

	client, err := smtp.NewClient(conn, s.cfg.Host)
	if err != nil {
		return fmt.Errorf("smtp client: %w", err)
	}
	defer client.Close()

	if !implicitTLS {
		if ok, _ := client.Extension("STARTTLS"); ok {
			if err := client.StartTLS(&tls.Config{ServerName: s.cfg.Host, MinVersion: tls.VersionTLS12}); err != nil {
				return fmt.Errorf("smtp starttls: %w", err)
			}
		}
	}
	if s.cfg.Username != "" {
		if err := client.Auth(smtp.PlainAuth("", s.cfg.Username, s.cfg.Password, s.cfg.Host)); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}
	if err := client.Mail(s.cfg.FromAddress); err != nil {
		return fmt.Errorf("smtp mail from: %w", err)
	}
	if err := client.Rcpt(m.To); err != nil {
		return fmt.Errorf("smtp rcpt: %w", err)
	}
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp data: %w", err)
	}
	if _, err := w.Write(buildMessage(s.cfg, m, time.Now())); err != nil {
		_ = w.Close()
		return fmt.Errorf("smtp write: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp close data: %w", err)
	}
	return client.Quit()
}

func buildMessage(cfg config.SMTP, m Message, now time.Time) []byte {
	boundary := randomBoundary()
	var b bytes.Buffer
	writeHeader := func(k, v string) {
		b.WriteString(k + ": " + v + "\r\n")
	}
	writeHeader("From", fmt.Sprintf("%q <%s>", cfg.FromName, cfg.FromAddress))
	writeHeader("To", m.To)
	writeHeader("Subject", mime.QEncoding.Encode("utf-8", m.Subject))
	writeHeader("Date", now.Format(time.RFC1123Z))
	writeHeader("MIME-Version", "1.0")
	writeHeader("Content-Type", `multipart/alternative; boundary="`+boundary+`"`)
	b.WriteString("\r\n")

	b.WriteString("--" + boundary + "\r\n")
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	b.WriteString("Content-Transfer-Encoding: 8bit\r\n\r\n")
	b.WriteString(strings.ReplaceAll(m.Text, "\n", "\r\n"))
	b.WriteString("\r\n")

	b.WriteString("--" + boundary + "\r\n")
	b.WriteString("Content-Type: text/html; charset=utf-8\r\n")
	b.WriteString("Content-Transfer-Encoding: 8bit\r\n\r\n")
	b.WriteString(strings.ReplaceAll(m.HTML, "\n", "\r\n"))
	b.WriteString("\r\n")

	b.WriteString("--" + boundary + "--\r\n")
	return b.Bytes()
}

func randomBoundary() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "stillalive-boundary"
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

var errNotConfigured = errors.New("smtp not configured")
