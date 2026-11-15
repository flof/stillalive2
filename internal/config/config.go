package config

import (
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

// SMTP holds outbound mail settings.
type SMTP struct {
	Host        string
	Port        int
	Username    string
	Password    string
	Encryption  string // "starttls", "tls" or "none"
	FromAddress string
	FromName    string
}

// Config is the complete runtime configuration, loaded from the environment.
type Config struct {
	AppName  string
	AppURL   string
	Addr     string
	LogLevel slog.Level

	DatabasePath string

	SessionSecure bool
	SessionTTL    time.Duration
	RememberTTL   time.Duration

	TokenLength        int
	MaxAlertEmails     int
	MinIntervalMinutes int
	ReminderInterval   time.Duration
	EvaluationInterval time.Duration
	PostPingURL        string
	TrustProxy         bool
	PingRetention      time.Duration

	SMTP SMTP
}

// Load reads configuration from environment variables, applying the same
// defaults as the original Laravel application where they matter.
func Load() Config {
	cfg := Config{
		AppName:            env("APP_NAME", "Still Alive"),
		AppURL:             strings.TrimRight(env("APP_URL", "http://localhost:8080"), "/"),
		Addr:               env("ADDR", ":8080"),
		LogLevel:           parseLevel(env("LOG_LEVEL", "info")),
		DatabasePath:       env("DATABASE_PATH", "stillalive.db"),
		SessionSecure:      envBool("SESSION_SECURE", false),
		SessionTTL:         envDuration("SESSION_TTL", 12*time.Hour),
		RememberTTL:        envDuration("REMEMBER_TTL", 30*24*time.Hour),
		TokenLength:        envInt("TOKEN_LENGTH", 16),
		MaxAlertEmails:     envInt("MAX_ALERT_EMAILS", 5),
		MinIntervalMinutes: envInt("MIN_INTERVAL_MINUTES", 5),
		ReminderInterval:   envDuration("REMINDER_INTERVAL", 24*time.Hour),
		EvaluationInterval: envDuration("EVALUATION_INTERVAL", time.Minute),
		PostPingURL:        env("STILLALIVE_POST_PING_URL", ""),
		TrustProxy:         envBool("TRUST_PROXY", false),
		PingRetention:      envDuration("PING_RETENTION", 0), // 0 = keep forever (v1 parity)
		SMTP: SMTP{
			Host:        env("SMTP_HOST", ""),
			Port:        envInt("SMTP_PORT", 587),
			Username:    env("SMTP_USERNAME", ""),
			Password:    env("SMTP_PASSWORD", ""),
			Encryption:  env("SMTP_ENCRYPTION", "starttls"),
			FromAddress: env("MAIL_FROM_ADDRESS", "stillalive@localhost"),
			FromName:    env("MAIL_FROM_NAME", "Still Alive"),
		},
	}
	if cfg.TokenLength < 16 {
		cfg.TokenLength = 16
	}
	if cfg.MaxAlertEmails < 1 {
		cfg.MaxAlertEmails = 1
	}
	if cfg.MinIntervalMinutes < 1 {
		cfg.MinIntervalMinutes = 1
	}
	return cfg
}

// LogMail reports whether mail should be written to the log instead of sent.
func (c Config) LogMail() bool { return c.SMTP.Host == "" }

func env(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	v, err := strconv.Atoi(env(key, ""))
	if err != nil {
		return fallback
	}
	return v
}

func envBool(key string, fallback bool) bool {
	v := strings.ToLower(env(key, ""))
	switch v {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func envDuration(key string, fallback time.Duration) time.Duration {
	v, err := time.ParseDuration(env(key, ""))
	if err != nil {
		return fallback
	}
	return v
}

func parseLevel(v string) slog.Level {
	switch strings.ToLower(v) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
