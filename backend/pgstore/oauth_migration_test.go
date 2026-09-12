package pgstore

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/jackc/logger4life/backend/core"
	"github.com/stretchr/testify/require"
)

func TestOAuthFamilyMigrationBackfillsExistingTokens(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newTestStore(t)
	user, err := store.CreateUser(ctx, uuid.NewV4().String(), "migration_user", nil, "hash")
	require.NoError(t, err)
	client := core.OAuthClient{ID: uuid.NewV7().String(), RedirectURIs: []string{"https://example.com/cb"}}
	require.NoError(t, store.CreateOAuthClient(ctx, client))
	pair := core.OAuthTokenPair{
		Grant:           core.OAuthGrant{ClientID: client.ID, UserID: user.ID, FamilyID: uuid.NewV7().String(), Scope: "mcp", Audience: "https://example.com"},
		AccessTokenHash: []byte("old-access-1"), RefreshTokenHash: []byte("old-refresh-1"),
		AccessExpiresAt: time.Now().Add(time.Hour), RefreshExpiresAt: time.Now().Add(time.Hour),
	}
	require.NoError(t, store.CreateTokenPair(ctx, pair))
	_, err = store.ConsumeRefreshToken(ctx, pair.RefreshTokenHash)
	require.NoError(t, err)
	pair.AccessTokenHash, pair.RefreshTokenHash = []byte("old-access-2"), []byte("old-refresh-2")
	require.NoError(t, store.CreateTokenPair(ctx, pair))
	accessOnly := pair
	accessOnly.Grant.FamilyID = uuid.NewV7().String()
	accessOnly.AccessTokenHash, accessOnly.RefreshTokenHash = []byte("access-only"), []byte("removed-refresh")
	require.NoError(t, store.CreateTokenPair(ctx, accessOnly))

	// Roll back the schema exercise so the test database retains its
	// pgundolog triggers and can be safely reused by the next test.
	tx, err := store.pool.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `DELETE FROM oauth_refresh_tokens WHERE token_hash = $1`, accessOnly.RefreshTokenHash)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `DROP TABLE oauth_token_families`)
	require.NoError(t, err)
	migration, err := os.ReadFile("../../postgresql/migrations/014_add_oauth_token_families.sql")
	require.NoError(t, err)
	up, _, found := strings.Cut(string(migration), "---- create above / drop below ----")
	require.True(t, found)
	_, err = tx.Exec(ctx, strings.ReplaceAll(up, "{{.app_user}}", "logger4life"))
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `SET LOCAL ROLE logger4life`)
	require.NoError(t, err)
	var count int
	err = tx.QueryRow(ctx, `SELECT count(*) FROM oauth_token_families WHERE revoked = false AND client_id = $1 AND user_id = $2`, client.ID, user.ID).Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 2, count, "backfill must deduplicate rotations and include access-only families")
	for _, hash := range [][]byte{pair.AccessTokenHash, accessOnly.AccessTokenHash} {
		err = tx.QueryRow(ctx, `SELECT count(*) FROM oauth_access_tokens a JOIN oauth_token_families f ON f.id = a.family_id WHERE a.token_hash = $1 AND f.revoked = false`, hash).Scan(&count)
		require.NoError(t, err)
		require.Equal(t, 1, count, "existing tokens must retain their family")
	}
}

func TestOAuthCodeGrantMigrationPreservesCodes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newTestStore(t)
	user, err := store.CreateUser(ctx, uuid.NewV4().String(), "code_migration_user", nil, "hash")
	require.NoError(t, err)
	client := core.OAuthClient{ID: uuid.NewV7().String(), RedirectURIs: []string{"http://localhost/cb"}}
	require.NoError(t, store.CreateOAuthClient(ctx, client))
	code := core.OAuthAuthorizationCode{ClientID: client.ID, UserID: user.ID, RedirectURI: client.RedirectURIs[0], Scope: "mcp", Audience: "https://logs.example.com", CodeChallenge: "challenge", CodeChallengeMethod: "S256", ExpiresAt: time.Now().Add(time.Hour)}
	require.NoError(t, store.CreateAuthorizationCode(ctx, []byte("old-code"), code))
	tx, err := store.pool.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `ALTER TABLE oauth_authorization_codes DROP COLUMN authorization_code_only`)
	require.NoError(t, err)
	migration, err := os.ReadFile("../../postgresql/migrations/016_oauth_code_grant_types.sql")
	require.NoError(t, err)
	up, _, found := strings.Cut(string(migration), "---- create above / drop below ----")
	require.True(t, found)
	_, err = tx.Exec(ctx, up)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `SET LOCAL ROLE logger4life`)
	require.NoError(t, err)
	var codeOnly bool
	var redirect string
	err = tx.QueryRow(ctx, `SELECT authorization_code_only, redirect_uri FROM oauth_authorization_codes WHERE code_hash = $1`, []byte("old-code")).Scan(&codeOnly, &redirect)
	require.NoError(t, err)
	require.False(t, codeOnly, "pre-migration DCR codes retain refresh support")
	require.Equal(t, code.RedirectURI, redirect)
}
