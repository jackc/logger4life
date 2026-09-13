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

func TestOAuthFamilyMigrationAddsColumnMissingFromEarlyDatabases(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := newTestStore(t)
	user, err := store.CreateUser(ctx, uuid.NewV4().String(), "early_migration_user", nil, "hash")
	require.NoError(t, err)
	client := core.OAuthClient{ID: uuid.NewV7().String(), RedirectURIs: []string{"https://example.com/cb"}}
	require.NoError(t, store.CreateOAuthClient(ctx, client))
	pair := core.OAuthTokenPair{
		Grant:           core.OAuthGrant{ClientID: client.ID, UserID: user.ID, FamilyID: uuid.NewV7().String(), Scope: "mcp", Audience: "https://example.com"},
		AccessTokenHash: []byte("early-access"), RefreshTokenHash: []byte("early-refresh"),
		AccessExpiresAt: time.Now().Add(time.Hour), RefreshExpiresAt: time.Now().Add(time.Hour),
	}
	require.NoError(t, store.CreateTokenPair(ctx, pair))
	accessOnly := pair
	accessOnly.Grant.FamilyID = uuid.NewV7().String()
	accessOnly.AccessTokenHash, accessOnly.RefreshTokenHash = []byte("early-access-only"), []byte("early-removed-refresh")
	require.NoError(t, store.CreateTokenPair(ctx, accessOnly))

	// Reproduce a database that applied 011 before it gained family_id, and
	// roll the schema exercise back so the test database keeps its pgundolog
	// triggers for the next test.
	tx, err := store.pool.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `DELETE FROM oauth_refresh_tokens WHERE token_hash = $1`, accessOnly.RefreshTokenHash)
	require.NoError(t, err)
	for _, statement := range []string{
		`DROP TABLE oauth_token_families`,
		`ALTER TABLE oauth_access_tokens DROP COLUMN family_id`,
		`ALTER TABLE oauth_refresh_tokens DROP COLUMN family_id`,
	} {
		_, err = tx.Exec(ctx, statement)
		require.NoError(t, err)
	}
	migration, err := os.ReadFile("../../postgresql/migrations/014_add_oauth_token_families.sql")
	require.NoError(t, err)
	up, _, found := strings.Cut(string(migration), "---- create above / drop below ----")
	require.True(t, found)
	_, err = tx.Exec(ctx, strings.ReplaceAll(up, "{{.app_user}}", "logger4life"))
	require.NoError(t, err)

	var restored int
	err = tx.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns
		WHERE table_name IN ('oauth_access_tokens', 'oauth_refresh_tokens')
			AND column_name = 'family_id' AND is_nullable = 'NO'`).Scan(&restored)
	require.NoError(t, err)
	require.Equal(t, 2, restored, "the migration must restore the column 011 was edited to create")
	err = tx.QueryRow(ctx, `SELECT count(*) FROM pg_indexes
		WHERE indexname IN ('oauth_access_tokens_family_idx', 'oauth_refresh_tokens_family_idx')`).Scan(&restored)
	require.NoError(t, err)
	require.Equal(t, 2, restored, "the column's indexes come with it")

	var shared bool
	err = tx.QueryRow(ctx, `SELECT a.family_id = r.family_id FROM oauth_access_tokens a
		JOIN oauth_refresh_tokens r ON r.token_hash = a.refresh_token_hash
		WHERE a.token_hash = $1`, pair.AccessTokenHash).Scan(&shared)
	require.NoError(t, err)
	require.True(t, shared, "a backfilled access token joins the family of the refresh token that issued it")

	_, err = tx.Exec(ctx, `SET LOCAL ROLE logger4life`)
	require.NoError(t, err)
	for _, hash := range [][]byte{pair.AccessTokenHash, accessOnly.AccessTokenHash} {
		var count int
		err = tx.QueryRow(ctx, `SELECT count(*) FROM oauth_access_tokens a
			JOIN oauth_token_families f ON f.id = a.family_id AND f.client_id = a.client_id AND f.user_id = a.user_id
			WHERE a.token_hash = $1 AND f.revoked = false`, hash).Scan(&count)
		require.NoError(t, err)
		require.Equal(t, 1, count, "every backfilled token gets a family of its own owner, unrevoked")
	}
}
