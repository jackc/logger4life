package backend

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	assert.False(t, cfg.AllowRegistration)
	assert.Equal(t, ":4000", cfg.ListenAddress)
	assert.Contains(t, cfg.DatabaseURL, "logger4life_dev")
}

func TestLoadConfigFile_AllowRegistrationTrue(t *testing.T) {
	f, err := os.CreateTemp("", "config-*.conf")
	require.NoError(t, err)
	defer os.Remove(f.Name())

	f.WriteString("allow_registration=true\n")
	f.Close()

	cfg, err := LoadConfigFile(f.Name())
	require.NoError(t, err)
	assert.True(t, cfg.AllowRegistration)
}

func TestLoadConfigFile_AllowRegistrationFalse(t *testing.T) {
	f, err := os.CreateTemp("", "config-*.conf")
	require.NoError(t, err)
	defer os.Remove(f.Name())

	f.WriteString("allow_registration=false\n")
	f.Close()

	cfg, err := LoadConfigFile(f.Name())
	require.NoError(t, err)
	assert.False(t, cfg.AllowRegistration)
}

func TestLoadConfigFile_MissingAllowRegistration(t *testing.T) {
	f, err := os.CreateTemp("", "config-*.conf")
	require.NoError(t, err)
	defer os.Remove(f.Name())

	f.WriteString("database_url=postgres://localhost/test\n")
	f.Close()

	cfg, err := LoadConfigFile(f.Name())
	require.NoError(t, err)
	assert.False(t, cfg.AllowRegistration)
}

func TestLoadConfigFile_CommentsAndBlanks(t *testing.T) {
	f, err := os.CreateTemp("", "config-*.conf")
	require.NoError(t, err)
	defer os.Remove(f.Name())

	f.WriteString("# This is a comment\n\nallow_registration=true\n")
	f.Close()

	cfg, err := LoadConfigFile(f.Name())
	require.NoError(t, err)
	assert.True(t, cfg.AllowRegistration)
}

func TestLoadConfigFile_AllKeys(t *testing.T) {
	f, err := os.CreateTemp("", "config-*.conf")
	require.NoError(t, err)
	defer os.Remove(f.Name())

	f.WriteString("database_url=postgres://localhost/mydb\nlisten_address=:8080\nallow_registration=true\n")
	f.Close()

	cfg, err := LoadConfigFile(f.Name())
	require.NoError(t, err)
	assert.Equal(t, "postgres://localhost/mydb", cfg.DatabaseURL)
	assert.Equal(t, ":8080", cfg.ListenAddress)
	assert.True(t, cfg.AllowRegistration)
}

func TestLoadConfigFile_FileNotFound(t *testing.T) {
	_, err := LoadConfigFile("/nonexistent/config.conf")
	assert.Error(t, err)
}
