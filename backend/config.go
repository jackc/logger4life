package backend

import (
	"log/slog"
	"net"
	"os"
	"strings"
)

type Config struct {
	DatabaseURL       string
	BindAddress       string
	Port              string
	AllowRegistration bool
	WebAuthnRPID      string
	WebAuthnOrigin    string
	LogLevel          string
	LogFormat         string
}

func DefaultConfig() Config {
	return Config{
		DatabaseURL:       "postgres://postgres:postgres@localhost:5432/logger4life_dev",
		BindAddress:       "127.0.0.1",
		Port:              "4000",
		AllowRegistration: false,
		WebAuthnRPID:      "",
		WebAuthnOrigin:    "",
		LogLevel:          "info",
		LogFormat:         "json",
	}
}

func (c Config) ListenAddress() string {
	return net.JoinHostPort(c.BindAddress, c.Port)
}

func (c Config) PasskeysEnabled() bool {
	return c.WebAuthnRPID != "" && c.WebAuthnOrigin != ""
}

func ConfigFromEnv() Config {
	cfg := DefaultConfig()

	if v := os.Getenv("DATABASE_URL"); v != "" {
		cfg.DatabaseURL = v
	}
	if v := os.Getenv("BIND_ADDRESS"); v != "" {
		cfg.BindAddress = v
	}
	if v := os.Getenv("PORT"); v != "" {
		cfg.Port = v
	}
	if v := os.Getenv("ALLOW_REGISTRATION"); v == "true" {
		cfg.AllowRegistration = true
	}
	if v := os.Getenv("WEBAUTHN_RP_ID"); v != "" {
		cfg.WebAuthnRPID = v
	}
	if v := os.Getenv("WEBAUTHN_ORIGIN"); v != "" {
		cfg.WebAuthnOrigin = v
	}
	if v := os.Getenv("LOG_LEVEL"); v != "" {
		cfg.LogLevel = v
	}
	if v := os.Getenv("LOG_FORMAT"); v != "" {
		cfg.LogFormat = v
	}

	return cfg
}

func (c Config) SlogLevel() slog.Level {
	switch strings.ToLower(c.LogLevel) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func (c Config) SlogHandler() slog.Handler {
	opts := &slog.HandlerOptions{Level: c.SlogLevel()}
	if strings.ToLower(c.LogFormat) == "text" {
		return slog.NewTextHandler(os.Stdout, opts)
	}
	return slog.NewJSONHandler(os.Stdout, opts)
}
