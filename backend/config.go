package backend

import (
	"log/slog"
	"net"
	"os"
	"strings"
	"unicode"

	slogjournal "github.com/systemd/slog-journal"
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
	MCPCanonicalURL   string
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
		MCPCanonicalURL:   "",
	}
}

func (c Config) ListenAddress() string {
	return net.JoinHostPort(c.BindAddress, c.Port)
}

func (c Config) PasskeysEnabled() bool {
	return c.WebAuthnRPID != "" && c.WebAuthnOrigin != ""
}

// MCPEnabled reports whether MCP+OAuth routes should be mounted. They require
// a canonical public URL to use as both the OAuth issuer and the RFC 8707
// audience for bearer tokens.
func (c Config) MCPEnabled() bool {
	return c.MCPCanonicalURL != ""
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
	if v := os.Getenv("MCP_CANONICAL_URL"); v != "" {
		cfg.MCPCanonicalURL = strings.TrimRight(v, "/")
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

func (c Config) SlogHandler() (slog.Handler, error) {
	switch strings.ToLower(c.LogFormat) {
	case "text":
		opts := &slog.HandlerOptions{Level: c.SlogLevel()}
		return slog.NewTextHandler(os.Stdout, opts), nil
	case "journal":
		return slogjournal.NewHandler(&slogjournal.Options{
			Level:        c.SlogLevel(),
			ReplaceAttr:  journalReplaceAttr,
			ReplaceGroup: normalizeJournalKey,
		})
	default:
		opts := &slog.HandlerOptions{Level: c.SlogLevel()}
		return slog.NewJSONHandler(os.Stdout, opts), nil
	}
}

// normalizeJournalKey converts a slog key or group name to a valid journald
// field name matching ^[A-Z_][A-Z0-9_]*$.
func normalizeJournalKey(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(unicode.ToUpper(r))
		} else {
			b.WriteByte('_')
		}
	}
	// Strip leading digits/underscores to ensure the key starts with a letter.
	result := strings.TrimLeftFunc(b.String(), func(r rune) bool {
		return !unicode.IsLetter(r)
	})
	if result == "" {
		return "UNKNOWN"
	}
	return result
}

func journalReplaceAttr(groups []string, a slog.Attr) slog.Attr {
	a.Key = normalizeJournalKey(a.Key)
	return a
}
