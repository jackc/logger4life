package jedstore

import (
	"context"
	"crypto/sha256"
	"testing"
	"time"
	"uuid"

	migrate "github.com/jackc/jed/migrate/go"
	jedmigrations "github.com/jackc/logger4life/db/migrations/jed"
	"github.com/stretchr/testify/require"
)

// Migration 006 replaces each stored session token with its SHA-256, so
// sessions created before it keep working.
func TestSessionHashMigrationPreservesSessions(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := Open(dir)
	require.NoError(t, err)
	t.Cleanup(func() {
		if store != nil {
			_ = store.Close()
		}
	})
	user, err := store.CreateUser(ctx, uuid.NewV4().String(), "session_migration_user", nil, "hash")
	require.NoError(t, err)
	migrations, err := migrate.LoadMigrationsFS(jedmigrations.FS, jedmigrations.Root)
	require.NoError(t, err)
	migrator, err := migrate.NewMigrator(store.db, migrations, migrate.Options{})
	require.NoError(t, err)
	require.NoError(t, migrator.MigrateTo(5))
	migrator.Close()
	token := []byte("a-session-token-from-before-006")
	_, err = store.db.Exec(ctx, `INSERT INTO sessions (id, user_id, token, expires_at) VALUES ($1, $2, $3, $4)`,
		uuid.NewV7().String(), user.ID, token, time.Now().Add(time.Hour))
	require.NoError(t, err)

	require.NoError(t, store.Close())
	store, err = Open(dir)
	require.NoError(t, err)
	hash := sha256.Sum256(token)
	found, err := store.GetUserBySessionToken(ctx, hash[:])
	require.NoError(t, err)
	require.Equal(t, user.ID, found.ID)
}
