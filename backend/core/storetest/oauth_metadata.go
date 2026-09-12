package storetest

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/logger4life/backend/core"
	"github.com/stretchr/testify/require"
)

func RunOAuthMetadataGrants(t *testing.T, ports Ports) {
	ctx := context.Background()
	user := newUser(t, ports)
	id := "https://example.com/" + newClientID() + ".json"
	code := core.OAuthAuthorizationCode{ClientID: id, UserID: user.ID, RedirectURI: "http://localhost/cb", Scope: "mcp", Audience: "https://logs.example.com", CodeChallenge: "challenge", CodeChallengeMethod: "S256", ExpiresAt: time.Now().Add(time.Hour), AuthorizationCodeOnly: true}
	hash := []byte(newClientID())
	require.NoError(t, ports.CreateMetadataAuthorizationCode(ctx, hash, code, core.OAuthDefaultMaxClients))
	identity, err := ports.GetOAuthClient(ctx, id)
	require.NoError(t, err)
	require.Equal(t, core.OAuthClient{ID: id, RedirectURIs: []string{}}, identity, "persist only URL identity, not cached metadata")
	stored, err := ports.ConsumeAuthorizationCode(ctx, hash)
	require.NoError(t, err)
	require.Equal(t, code.RedirectURI, stored.RedirectURI)
	require.True(t, stored.AuthorizationCodeOnly, "snapshot grant policy through storage")
	// Existing identities work at capacity, and new identities cannot exceed it.
	require.NoError(t, ports.CreateMetadataAuthorizationCode(ctx, []byte(newClientID()), code, 0))
	code.ClientID = "https://example.com/" + newClientID()
	require.ErrorIs(t, ports.CreateMetadataAuthorizationCode(ctx, []byte(newClientID()), code, 0), core.ErrOAuthClientLimit)
	_, err = ports.GetOAuthClient(ctx, code.ClientID)
	require.ErrorIs(t, err, core.ErrOAuthRecordNotFound)
	// Identity admission must roll back if code issuance fails (e.g. account
	// deleted concurrently), rather than leaving unused records behind.
	code.UserID = UnknownID
	require.Error(t, ports.CreateMetadataAuthorizationCode(ctx, []byte(newClientID()), code, core.OAuthDefaultMaxClients))
	_, err = ports.GetOAuthClient(ctx, code.ClientID)
	require.ErrorIs(t, err, core.ErrOAuthRecordNotFound)
	// Access-only grants use a family for durable revocation bookkeeping but
	// have no refresh row, and are usable through the ordinary bearer path.
	pair := core.OAuthTokenPair{Grant: core.OAuthGrant{ClientID: id, UserID: user.ID, FamilyID: newRowID(), Scope: "mcp", Audience: "https://logs.example.com"}, AccessTokenHash: []byte(newClientID()), AccessExpiresAt: time.Now().Add(time.Hour)}
	require.NoError(t, ports.CreateTokenPair(ctx, pair))
	grant, err := ports.GetGrantByAccessToken(ctx, pair.AccessTokenHash)
	require.NoError(t, err)
	require.Equal(t, id, grant.ClientID)
	require.NoError(t, ports.RevokeAccessToken(ctx, pair.AccessTokenHash))
	_, err = ports.GetGrantByAccessToken(ctx, pair.AccessTokenHash)
	require.ErrorIs(t, err, core.ErrOAuthRecordNotFound)
}

// RunOAuthMetadataClientLimits requires an empty store and runs each adapter
// independently, since concurrent admission order differs between databases.
func RunOAuthMetadataClientLimits(t *testing.T, ports Ports) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	user := newUser(t, ports)
	code := core.OAuthAuthorizationCode{ClientID: "https://example.com/client.json", UserID: user.ID, RedirectURI: "http://localhost/cb", Scope: "mcp", Audience: "https://logs.example.com", CodeChallenge: "challenge", CodeChallengeMethod: "S256", ExpiresAt: time.Now().Add(time.Hour)}
	var wg sync.WaitGroup
	errs := make(chan error, 40)
	for i := range 20 {
		wg.Go(func() { errs <- ports.CreateMetadataAuthorizationCode(ctx, []byte(fmt.Sprintf("code-%d", i)), code, 1) })
		wg.Go(func() { _, err := ports.PruneUnusedOAuthClients(ctx, time.Now().Add(time.Hour)); errs <- err })
	}
	wg.Wait()
	for range 40 {
		require.NoError(t, <-errs, "same identity admitted once and retained through issuance")
	}
	for i := range 20 {
		_, err := ports.ConsumeAuthorizationCode(ctx, []byte(fmt.Sprintf("code-%d", i)))
		require.NoError(t, err)
	}
	var admitted atomic.Int32
	for i := range 20 {
		wg.Go(func() {
			var err error
			if i%2 == 0 {
				err = ports.CreateOAuthClientLimited(ctx, core.OAuthClient{ID: newClientID(), RedirectURIs: []string{"http://localhost/cb"}}, 3)
			} else {
				c := code
				c.ClientID = fmt.Sprintf("https://example.com/%d.json", i)
				err = ports.CreateMetadataAuthorizationCode(ctx, []byte(fmt.Sprintf("new-code-%d", i)), c, 3)
			}
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
	require.EqualValues(t, 2, admitted.Load(), "DCR and CIMD share the same atomic total quota")
}
