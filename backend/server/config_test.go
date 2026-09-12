package server

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	assert.Equal(t, "postgresql", cfg.DatabaseBackend)
	assert.Empty(t, cfg.JedDataDir)
	assert.False(t, cfg.AllowRegistration)
	assert.Equal(t, "127.0.0.1", cfg.BindAddress)
	assert.Equal(t, "4000", cfg.Port)
	assert.Equal(t, "127.0.0.1:4000", cfg.ListenAddress())
	assert.Contains(t, cfg.DatabaseURL, "logger4life_dev")
}

func TestLimitConfigValidation(t *testing.T) {
	for _, name := range []string{"MCP_REQUESTS_PER_MINUTE", "MCP_REQUEST_BURST", "SQL_CONCURRENCY_PER_USER", "SQL_CONCURRENCY_GLOBAL", "OAUTH_REGISTRATION_PER_IP", "OAUTH_REGISTRATION_GLOBAL", "OAUTH_MAX_CLIENTS", "OAUTH_UNUSED_CLIENT_HOURS"} {
		t.Run(name, func(t *testing.T) {
			for _, value := range []string{"", "0", "-1", "lots", "1000001"} {
				t.Setenv(name, value)
				assert.ErrorContains(t, ConfigFromEnv().validateLimits(), name)
			}
			t.Setenv(name, "12")
			assert.NoError(t, ConfigFromEnv().validateLimits())
		})
	}
	t.Setenv("TRUSTED_PROXY_CIDRS", "localhost")
	assert.ErrorContains(t, ConfigFromEnv().validateLimits(), "TRUSTED_PROXY_CIDRS")
}

func TestConfigFromEnv(t *testing.T) {
	t.Setenv("DATABASE_BACKEND", "jed")
	t.Setenv("DATABASE_URL", "postgres://localhost/mydb")
	t.Setenv("JED_DATA_DIR", "/srv/logger4life")
	t.Setenv("BIND_ADDRESS", "0.0.0.0")
	t.Setenv("PORT", "8080")
	t.Setenv("ALLOW_REGISTRATION", "true")
	t.Setenv("WEBAUTHN_RP_ID", "example.com")
	t.Setenv("WEBAUTHN_ORIGIN", "https://example.com")

	cfg := ConfigFromEnv()
	assert.Equal(t, "jed", cfg.DatabaseBackend)
	assert.Equal(t, "postgres://localhost/mydb", cfg.DatabaseURL)
	assert.Equal(t, "/srv/logger4life", cfg.JedDataDir)
	assert.Equal(t, "0.0.0.0", cfg.BindAddress)
	assert.Equal(t, "8080", cfg.Port)
	assert.Equal(t, "0.0.0.0:8080", cfg.ListenAddress())
	assert.True(t, cfg.AllowRegistration)
	assert.Equal(t, "example.com", cfg.WebAuthnRPID)
	assert.Equal(t, "https://example.com", cfg.WebAuthnOrigin)
	assert.True(t, cfg.PasskeysEnabled())
}

func TestConfigFromEnv_Defaults(t *testing.T) {
	cfg := ConfigFromEnv()
	assert.Equal(t, "postgresql", cfg.DatabaseBackend)
	assert.Empty(t, cfg.JedDataDir)
	assert.Contains(t, cfg.DatabaseURL, "logger4life_dev")
	assert.Equal(t, "127.0.0.1", cfg.BindAddress)
	assert.Equal(t, "4000", cfg.Port)
	assert.False(t, cfg.AllowRegistration)
	assert.Empty(t, cfg.WebAuthnRPID)
	assert.Empty(t, cfg.WebAuthnOrigin)
	assert.False(t, cfg.PasskeysEnabled())
}

func TestConfigFromEnv_AllowRegistrationFalse(t *testing.T) {
	t.Setenv("ALLOW_REGISTRATION", "false")

	cfg := ConfigFromEnv()
	assert.False(t, cfg.AllowRegistration)
}

func TestNormalizeJournalKey(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"time", "TIME"},
		{"msg", "MSG"},
		{"http.request.method", "HTTP_REQUEST_METHOD"},
		{"@timestamp", "TIMESTAMP"},
		{"user_agent.original", "USER_AGENT_ORIGINAL"},
		{"log.level", "LOG_LEVEL"},
		{"simple", "SIMPLE"},
		{"ALREADY_UPPER", "ALREADY_UPPER"},
		{"123leading_digits", "LEADING_DIGITS"},
		{"_leading_underscore", "LEADING_UNDERSCORE"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.expected, normalizeJournalKey(tt.input))
		})
	}
}
