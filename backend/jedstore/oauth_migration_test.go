package jedstore

import (
	"context"
	"testing"
	"time"
	"uuid"

	migrate "github.com/jackc/jed/migrate/go"
	"github.com/jackc/logger4life/backend/core"
	jedmigrations "github.com/jackc/logger4life/db/migrations/jed"
	"github.com/stretchr/testify/require"
)

func TestOAuthFamilyMigrationPreservesTokensAndRevocation(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := Open(dir)
	require.NoError(t, err)
	t.Cleanup(func() {
		if store != nil {
			_ = store.Close()
		}
	})
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
	replay := pair.RefreshTokenHash
	_, err = store.ConsumeRefreshToken(ctx, replay)
	require.NoError(t, err)
	pair.AccessTokenHash, pair.RefreshTokenHash = []byte("old-access-2"), []byte("old-refresh-2")
	require.NoError(t, store.CreateTokenPair(ctx, pair))
	accessOnly := pair
	accessOnly.Grant.FamilyID = uuid.NewV7().String()
	accessOnly.AccessTokenHash, accessOnly.RefreshTokenHash = []byte("access-only"), []byte("removed-refresh")
	require.NoError(t, store.CreateTokenPair(ctx, accessOnly))
	_, err = store.db.Exec(ctx, `DELETE FROM oauth_refresh_tokens WHERE token_hash = $1`, accessOnly.RefreshTokenHash)
	require.NoError(t, err)

	// Restore the old schema with an already-rotated family and a family
	// represented only by an access token. Reopening must upgrade this data.
	migrations, err := migrate.LoadMigrationsFS(jedmigrations.FS, jedmigrations.Root)
	require.NoError(t, err)
	migrator, err := migrate.NewMigrator(store.db, migrations, migrate.Options{})
	require.NoError(t, err)
	defer migrator.Close()
	require.NoError(t, migrator.MigrateTo(1))
	migrator.Close()
	require.NoError(t, store.Close())
	store, err = Open(dir)
	require.NoError(t, err)
	for _, hash := range [][]byte{pair.AccessTokenHash, accessOnly.AccessTokenHash} {
		grant, err := store.GetGrantByAccessToken(ctx, hash)
		require.NoError(t, err)
		require.Equal(t, user.ID, grant.UserID)
	}

	_, err = store.ConsumeRefreshToken(ctx, replay)
	require.ErrorIs(t, err, core.ErrOAuthRefreshReuse)
	require.NoError(t, store.Close())
	store, err = Open(dir)
	require.NoError(t, err)
	_, err = store.GetGrantByAccessToken(ctx, pair.AccessTokenHash)
	require.ErrorIs(t, err, core.ErrOAuthRecordNotFound)
	pair.AccessTokenHash, pair.RefreshTokenHash = []byte("late-access"), []byte("late-refresh")
	require.ErrorIs(t, store.CreateTokenPair(ctx, pair), core.ErrOAuthRefreshReuse, "revocation must survive reopening")
	_, err = store.GetGrantByAccessToken(ctx, accessOnly.AccessTokenHash)
	require.NoError(t, err, "revocation must not affect another family")
}

func TestOAuthCodeGrantMigrationPreservesCodes(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := Open(dir)
	require.NoError(t, err)
	t.Cleanup(func() {
		if store != nil {
			_ = store.Close()
		}
	})
	user, err := store.CreateUser(ctx, uuid.NewV4().String(), "code_migration_user", nil, "hash")
	require.NoError(t, err)
	client := core.OAuthClient{ID: uuid.NewV7().String(), RedirectURIs: []string{"http://localhost/cb"}}
	require.NoError(t, store.CreateOAuthClient(ctx, client))
	code := core.OAuthAuthorizationCode{ClientID: client.ID, UserID: user.ID, RedirectURI: client.RedirectURIs[0], Scope: "mcp", Audience: "https://logs.example.com", CodeChallenge: "challenge", CodeChallengeMethod: "S256", ExpiresAt: time.Now().Add(time.Hour)}
	require.NoError(t, store.CreateAuthorizationCode(ctx, []byte("old-code"), code))
	migrations, err := migrate.LoadMigrationsFS(jedmigrations.FS, jedmigrations.Root)
	require.NoError(t, err)
	migrator, err := migrate.NewMigrator(store.db, migrations, migrate.Options{})
	require.NoError(t, err)
	require.NoError(t, migrator.MigrateTo(3))
	migrator.Close()
	require.NoError(t, store.Close())
	store, err = Open(dir)
	require.NoError(t, err)
	old, err := store.ConsumeAuthorizationCode(ctx, []byte("old-code"))
	require.NoError(t, err)
	require.False(t, old.AuthorizationCodeOnly, "pre-migration DCR codes retain refresh support")
	require.Equal(t, code.RedirectURI, old.RedirectURI)
	code.ClientID = "https://example.com/client.json"
	code.AuthorizationCodeOnly = true
	require.NoError(t, store.CreateMetadataAuthorizationCode(ctx, []byte("new-code"), code, 10))
	require.NoError(t, store.Close())
	store, err = Open(dir)
	require.NoError(t, err)
	current, err := store.ConsumeAuthorizationCode(ctx, []byte("new-code"))
	require.NoError(t, err)
	require.True(t, current.AuthorizationCodeOnly, "approved grant policy survives restart without metadata cache")
}
