package server

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	assert.False(t, cfg.AllowRegistration)
	assert.Equal(t, "127.0.0.1", cfg.BindAddress)
	assert.Equal(t, "4000", cfg.Port)
	assert.Equal(t, "127.0.0.1:4000", cfg.ListenAddress())
	assert.Contains(t, cfg.DatabaseURL, "logger4life_dev")
}

func TestConfigFromEnv(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/mydb")
	t.Setenv("BIND_ADDRESS", "0.0.0.0")
	t.Setenv("PORT", "8080")
	t.Setenv("ALLOW_REGISTRATION", "true")
	t.Setenv("WEBAUTHN_RP_ID", "example.com")
	t.Setenv("WEBAUTHN_ORIGIN", "https://example.com")

	cfg := ConfigFromEnv()
	assert.Equal(t, "postgres://localhost/mydb", cfg.DatabaseURL)
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
