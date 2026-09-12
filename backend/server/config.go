package server

import (
	"fmt"
	"log/slog"
	"net"
	"os"
	"strconv"
	"strings"
	"unicode"

	slogjournal "github.com/systemd/slog-journal"
)

type Config struct {
	OAuthRegistrationPerIP  int
	OAuthRegistrationGlobal int
	TrustedProxyCIDRs       string
	SQLConcurrencyPerUser   int
	SQLConcurrencyGlobal    int
	// DatabaseBackend selects "postgresql" (default), "jed", or the
	// fail-stop comparison harness "both".
	DatabaseBackend string
	DatabaseURL     string
	// JedDataDir holds logger4life.jed and is required by the jed and both backends.
	JedDataDir           string
	BindAddress          string
	Port                 string
	AllowRegistration    bool
	WebAuthnRPID         string
	WebAuthnOrigin       string
	LogLevel             string
	LogFormat            string
	MCPCanonicalURL      string
	SecureCookies        bool
	MCPRequestsPerMinute int
	MCPRequestBurst      int
}

func DefaultConfig() Config {
	return Config{
		OAuthRegistrationPerIP:  5,
		OAuthRegistrationGlobal: 30,
		SQLConcurrencyPerUser:   2,
		SQLConcurrencyGlobal:    8,
		DatabaseBackend:         "postgresql",
		DatabaseURL:             "postgres://postgres:postgres@localhost:5432/logger4life_dev",
		JedDataDir:              "",
		BindAddress:             "127.0.0.1",
		Port:                    "4000",
		AllowRegistration:       false,
		WebAuthnRPID:            "",
		WebAuthnOrigin:          "",
		LogLevel:                "info",
		LogFormat:               "json",
		MCPCanonicalURL:         "",
		SecureCookies:           false,
		MCPRequestsPerMinute:    60,
		MCPRequestBurst:         10,
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

	if v := os.Getenv("DATABASE_BACKEND"); v != "" {
		cfg.DatabaseBackend = v
	}
	if v := os.Getenv("DATABASE_URL"); v != "" {
		cfg.DatabaseURL = v
	}
	if v := os.Getenv("JED_DATA_DIR"); v != "" {
		cfg.JedDataDir = v
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
	if v := os.Getenv("SECURE_COOKIES"); v == "true" {
		cfg.SecureCookies = true
	}
	readLimitEnv("SQL_CONCURRENCY_PER_USER", &cfg.SQLConcurrencyPerUser)
	readLimitEnv("SQL_CONCURRENCY_GLOBAL", &cfg.SQLConcurrencyGlobal)
	cfg.TrustedProxyCIDRs = os.Getenv("TRUSTED_PROXY_CIDRS")
	readLimitEnv("OAUTH_REGISTRATION_PER_IP", &cfg.OAuthRegistrationPerIP)
	readLimitEnv("OAUTH_REGISTRATION_GLOBAL", &cfg.OAuthRegistrationGlobal)
	readLimitEnv("MCP_REQUESTS_PER_MINUTE", &cfg.MCPRequestsPerMinute)
	readLimitEnv("MCP_REQUEST_BURST", &cfg.MCPRequestBurst)

	return cfg
}

func readLimitEnv(name string, target *int) {
	if value, ok := os.LookupEnv(name); ok {
		n, err := strconv.Atoi(value)
		if err != nil || n <= 0 || n > 1000000 {
			*target = -1 // Report invalid configuration before opening the database.
		} else {
			*target = n
		}
	}
}

func (c Config) validateLimits() error {
	if _, err := parseTrustedProxies(c.TrustedProxyCIDRs); err != nil {
		return err
	}
	for name, value := range map[string]int{"OAUTH_REGISTRATION_PER_IP": c.OAuthRegistrationPerIP, "OAUTH_REGISTRATION_GLOBAL": c.OAuthRegistrationGlobal, "SQL_CONCURRENCY_PER_USER": c.SQLConcurrencyPerUser, "SQL_CONCURRENCY_GLOBAL": c.SQLConcurrencyGlobal, "MCP_REQUESTS_PER_MINUTE": c.MCPRequestsPerMinute, "MCP_REQUEST_BURST": c.MCPRequestBurst} {
		if value < 0 || value > 1000000 {
			return fmt.Errorf("%s must be an integer between 1 and 1000000", name)
		}
	}
	return nil
}

func limitOrDefault(value, fallback int) int {
	if value == 0 {
		return fallback
	}
	return value
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
