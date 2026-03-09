package backend

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	assert.False(t, cfg.AllowRegistration)
	assert.Equal(t, ":4000", cfg.ListenAddress)
	assert.Contains(t, cfg.DatabaseURL, "logger4life_dev")
}

func TestConfigFromEnv(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/mydb")
	t.Setenv("LISTEN_ADDRESS", ":8080")
	t.Setenv("ALLOW_REGISTRATION", "true")
	t.Setenv("WEBAUTHN_RP_ID", "example.com")
	t.Setenv("WEBAUTHN_ORIGIN", "https://example.com")

	cfg := ConfigFromEnv()
	assert.Equal(t, "postgres://localhost/mydb", cfg.DatabaseURL)
	assert.Equal(t, ":8080", cfg.ListenAddress)
	assert.True(t, cfg.AllowRegistration)
	assert.Equal(t, "example.com", cfg.WebAuthnRPID)
	assert.Equal(t, "https://example.com", cfg.WebAuthnOrigin)
	assert.True(t, cfg.PasskeysEnabled())
}

func TestConfigFromEnv_Defaults(t *testing.T) {
	cfg := ConfigFromEnv()
	assert.Contains(t, cfg.DatabaseURL, "logger4life_dev")
	assert.Equal(t, ":4000", cfg.ListenAddress)
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
