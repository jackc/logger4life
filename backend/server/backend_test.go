package server

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/jackc/logger4life/backend/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildBackendJedPersistsAcrossReopen(t *testing.T) {
	ctx := context.Background()
	cfg := DefaultConfig()
	cfg.DatabaseBackend = "jed"
	cfg.JedDataDir = t.TempDir()
	logger := slog.New(slog.DiscardHandler)

	app, health, cleanup, err := BuildBackend(ctx, cfg, logger)
	require.NoError(t, err)
	firstCleanup := cleanup
	firstClosed := false
	t.Cleanup(func() {
		if !firstClosed {
			firstCleanup()
		}
	})
	require.NoError(t, health(ctx))

	registered, err := core.RegisterUser.Call(ctx, app, core.RegisterUserParams{
		Username: "embedded_user",
		Password: "correct horse battery staple",
	})
	require.NoError(t, err)
	require.NotEmpty(t, registered.Token)
	firstCleanup()
	firstClosed = true

	assert.FileExists(t, filepath.Join(cfg.JedDataDir, "logger4life.jed"))
	app, health, cleanup, err = BuildBackend(ctx, cfg, logger)
	require.NoError(t, err)
	t.Cleanup(cleanup)
	require.NoError(t, health(ctx))

	loggedIn, err := core.LoginWithPassword.Call(ctx, app, core.LoginWithPasswordParams{
		Username: "embedded_user",
		Password: "correct horse battery staple",
	})
	require.NoError(t, err)
	assert.Equal(t, registered.User.ID, loggedIn.User.ID)
}

func TestBuildBackendRejectsInvalidJedConfiguration(t *testing.T) {
	cfg := DefaultConfig()
	cfg.DatabaseBackend = "jed"

	_, _, cleanup, err := BuildBackend(context.Background(), cfg, slog.New(slog.DiscardHandler))
	require.ErrorContains(t, err, "JED_DATA_DIR is required")
	cleanup()

	cfg.DatabaseBackend = "both"
	_, _, cleanup, err = BuildBackend(context.Background(), cfg, slog.New(slog.DiscardHandler))
	require.ErrorContains(t, err, "JED_DATA_DIR is required")
	cleanup()

	cfg.DatabaseBackend = "sqlite"
	_, _, cleanup, err = BuildBackend(context.Background(), cfg, slog.New(slog.DiscardHandler))
	require.ErrorContains(t, err, `unknown DATABASE_BACKEND "sqlite"`)
	cleanup()
}
