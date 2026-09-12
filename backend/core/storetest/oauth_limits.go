package storetest

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/logger4life/backend/core"
	"github.com/stretchr/testify/require"
)

// RunOAuthClientLimits requires an empty store and runs each real adapter
// independently: concurrent scheduling is not comparable in dualstore.
func RunOAuthClientLimits(t *testing.T, ports Ports) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var admitted atomic.Int32
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for range 20 {
		wg.Go(func() {
			err := ports.CreateOAuthClientLimited(ctx, core.OAuthClient{ID: newClientID(), RedirectURIs: []string{"https://example.com/cb"}}, 3)
			if err == nil {
				admitted.Add(1)
			} else if !errors.Is(err, core.ErrOAuthClientLimit) {
				errs <- err
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	require.EqualValues(t, 3, admitted.Load(), "count-and-insert must be atomic")
	n, err := ports.PruneUnusedOAuthClients(ctx, time.Now().Add(-24*time.Hour))
	require.NoError(t, err)
	require.Zero(t, n, "recent registrations must survive")
	n, err = ports.PruneUnusedOAuthClients(ctx, time.Now().Add(time.Hour))
	require.NoError(t, err)
	require.EqualValues(t, 3, n)
	user := newUser(t, ports)
	client := core.OAuthClient{ID: newClientID(), RedirectURIs: []string{"https://example.com/cb"}}
	require.NoError(t, ports.CreateOAuthClientLimited(ctx, client, 3), "cleanup must free capacity")
	code := core.OAuthAuthorizationCode{ClientID: client.ID, UserID: user.ID, RedirectURI: client.RedirectURIs[0], Scope: "mcp", Audience: "https://example.com", CodeChallenge: "challenge", CodeChallengeMethod: "S256", ExpiresAt: time.Now().Add(-time.Hour)}
	require.NoError(t, ports.CreateAuthorizationCode(ctx, []byte("old-code"), code))
	n, err = ports.PruneUnusedOAuthClients(ctx, time.Now().Add(time.Hour))
	require.NoError(t, err)
	require.Zero(t, n, "even expired authorization history is preserved")
	for i := range 20 {
		client.ID = newClientID()
		require.NoError(t, ports.CreateOAuthClient(ctx, client))
		pair := core.OAuthTokenPair{Grant: core.OAuthGrant{ClientID: client.ID, UserID: user.ID, FamilyID: testUUID(client.ID), Scope: "mcp", Audience: "https://example.com"}, AccessTokenHash: []byte(client.ID + "-access"), RefreshTokenHash: []byte(client.ID + "-refresh"), AccessExpiresAt: time.Now().Add(time.Hour), RefreshExpiresAt: time.Now().Add(time.Hour)}
		start := make(chan struct{})
		issued, cleaned := make(chan error, 1), make(chan error, 1)
		go func() { <-start; issued <- ports.CreateTokenPair(ctx, pair) }()
		go func() {
			<-start
			_, err := ports.PruneUnusedOAuthClients(ctx, time.Now().Add(time.Hour))
			cleaned <- err
		}()
		close(start)
		require.NoError(t, <-cleaned)
		if err := <-issued; err == nil {
			_, err = ports.GetGrantByAccessToken(ctx, pair.AccessTokenHash)
			require.NoError(t, err, "cleanup revoked concurrent issuance on iteration %d", i)
			require.NoError(t, ports.RevokeRefreshToken(ctx, pair.RefreshTokenHash))
			_, err = ports.PruneUnusedOAuthClients(ctx, time.Now().Add(time.Hour))
			require.NoError(t, err)
			pair.AccessTokenHash, pair.RefreshTokenHash = []byte(client.ID+"-late-access"), []byte(client.ID+"-late-refresh")
			require.ErrorIs(t, ports.CreateTokenPair(ctx, pair), core.ErrOAuthRefreshReuse, "cleanup must retain revocation history")
		} else {
			_, lookupErr := ports.GetOAuthClient(ctx, client.ID)
			require.ErrorIs(t, lookupErr, core.ErrOAuthRecordNotFound, "issuance can fail only if cleanup removed its client: %v", err)
		}
	}
}

func RunOAuthClientRetention(t *testing.T, ports Ports) {
	ctx := context.Background()
	client := core.OAuthClient{ID: newClientID(), RedirectURIs: []string{"https://example.com/cb"}}
	require.ErrorIs(t, ports.CreateOAuthClientLimited(ctx, client, 0), core.ErrOAuthClientLimit)
	require.NoError(t, ports.CreateOAuthClientLimited(ctx, client, core.OAuthDefaultMaxClients))
	_, err := ports.PruneUnusedOAuthClients(ctx, time.Unix(0, 0))
	require.NoError(t, err)
	_, err = ports.GetOAuthClient(ctx, client.ID)
	require.NoError(t, err)
	_, err = ports.PruneUnusedOAuthClients(ctx, time.Now().Add(time.Hour))
	require.NoError(t, err)
	_, err = ports.GetOAuthClient(ctx, client.ID)
	require.ErrorIs(t, err, core.ErrOAuthRecordNotFound)
}
