package pgstore

import (
	"context"
	"crypto/sha256"
	"os"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/stretchr/testify/require"
)

// Migration 018 replaces each stored session token with its SHA-256, so
// sessions created before it keep working.
func TestSessionHashMigrationPreservesSessions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newTestStore(t)
	user, err := store.CreateUser(ctx, uuid.NewV4().String(), "session_migration_user", nil, "hash")
	require.NoError(t, err)
	migration, err := os.ReadFile("../../postgresql/migrations/018_hash_session_tokens.sql")
	require.NoError(t, err)
	up, down, found := strings.Cut(string(migration), "---- create above / drop below ----")
	require.True(t, found)

	// Roll back the schema exercise so the test database retains its
	// pgundolog triggers and can be safely reused by the next test.
	tx, err := store.pool.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, down)
	require.NoError(t, err)
	token := []byte("a-session-token-from-before-018")
	_, err = tx.Exec(ctx, `INSERT INTO sessions (user_id, token, expires_at) VALUES ($1, $2, $3)`, user.ID, token, time.Now().Add(time.Hour))
	require.NoError(t, err)
	_, err = tx.Exec(ctx, up)
	require.NoError(t, err)
	var stored []byte
	require.NoError(t, tx.QueryRow(ctx, `SELECT token_hash FROM sessions WHERE user_id = $1`, user.ID).Scan(&stored))
	hash := sha256.Sum256(token)
	require.Equal(t, hash[:], stored)
}
