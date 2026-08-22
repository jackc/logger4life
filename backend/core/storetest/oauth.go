package storetest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/logger4life/backend/core"
)

// RunOAuthStore checks the port behind the OAuth 2.1 flow that lets an MCP
// client act for a user.
//
// Almost all of its contract is about single use. An authorization code and a
// refresh token are bearer credentials that travel through a browser and a
// client, so the store — not the caller — has to make redeeming one atomic:
// two racing redemptions must not both succeed. Reuse of an already-rotated
// refresh token is treated as evidence of theft and takes down the whole
// family descended from that authorization.
func RunOAuthStore(t *testing.T, ports Ports) {
	ctx := context.Background()

	newClient := func(t *testing.T, label string) core.OAuthClient {
		t.Helper()
		client := core.OAuthClient{
			ID:           newClientID(),
			RedirectURIs: []string{"https://example.com/callback"},
			ClientName:   "Test Client",
		}
		if err := ports.CreateOAuthClient(ctx, client); err != nil {
			t.Fatalf("creating fixture client: %v", err)
		}
		return client
	}

	newGrant := func(client core.OAuthClient, user core.User, label string) core.OAuthGrant {
		return core.OAuthGrant{
			ClientID: client.ID, UserID: user.ID, Scope: core.OAuthScopeMCP,
			Audience: "https://example.com/mcp", FamilyID: testUUID("family-" + label),
		}
	}

	t.Run("round trips a client", func(t *testing.T) {
		client := newClient(t, "roundtrip")

		found, err := ports.GetOAuthClient(ctx, client.ID)
		if err != nil {
			t.Fatal(err)
		}
		if found.ID != client.ID || found.ClientName != "Test Client" {
			t.Errorf("GetOAuthClient = %#v, want the client that was registered", found)
		}
		if len(found.RedirectURIs) != 1 || found.RedirectURIs[0] != client.RedirectURIs[0] {
			t.Errorf("RedirectURIs = %#v, want the URIs registered", found.RedirectURIs)
		}
	})

	// A client registered without a name comes back with an empty one rather
	// than a null the adapter would have to special-case.
	t.Run("round trips a client with no name", func(t *testing.T) {
		client := core.OAuthClient{ID: newClientID(), RedirectURIs: []string{"https://example.com/cb"}}
		if err := ports.CreateOAuthClient(ctx, client); err != nil {
			t.Fatal(err)
		}
		found, err := ports.GetOAuthClient(ctx, client.ID)
		if err != nil {
			t.Fatal(err)
		}
		if found.ClientName != "" {
			t.Errorf("ClientName = %q, want an empty string", found.ClientName)
		}
	})

	t.Run("reports an unknown client rather than a zero one", func(t *testing.T) {
		if _, err := ports.GetOAuthClient(ctx, Prefix+"absent"); !errors.Is(err, core.ErrOAuthRecordNotFound) {
			t.Errorf("GetOAuthClient error = %v, want ErrOAuthRecordNotFound", err)
		}
	})

	// The code carries the PKCE challenge, which is what stops an intercepted
	// code being redeemed by anyone but the client that requested it, so it
	// has to survive the round trip intact.
	t.Run("round trips an authorization code and consumes it once", func(t *testing.T) {
		client := newClient(t, "code")
		user := newUser(t, ports)
		hash := []byte("code-hash-" + user.Username)
		expires := time.Now().Add(core.OAuthAuthorizationCodeLifespan).UTC().Truncate(time.Microsecond)

		code := core.OAuthAuthorizationCode{
			ClientID: client.ID, UserID: user.ID, RedirectURI: client.RedirectURIs[0],
			Scope: core.OAuthScopeMCP, Audience: "https://example.com/mcp",
			CodeChallenge: "challenge", CodeChallengeMethod: "S256", ExpiresAt: expires,
		}
		if err := ports.CreateAuthorizationCode(ctx, hash, code); err != nil {
			t.Fatal(err)
		}

		consumed, err := ports.ConsumeAuthorizationCode(ctx, hash)
		if err != nil {
			t.Fatal(err)
		}
		if consumed.ClientID != client.ID || consumed.UserID != user.ID || consumed.RedirectURI != code.RedirectURI {
			t.Errorf("consumed %#v, want the code that was issued", consumed)
		}
		if consumed.CodeChallenge != "challenge" || consumed.CodeChallengeMethod != "S256" {
			t.Errorf("the PKCE challenge did not survive: %#v", consumed)
		}
		if consumed.Scope != core.OAuthScopeMCP || consumed.Audience != code.Audience {
			t.Errorf("consumed %#v, want the scope and audience issued", consumed)
		}

		// The replay defense.
		if _, err := ports.ConsumeAuthorizationCode(ctx, hash); !errors.Is(err, core.ErrOAuthRecordNotFound) {
			t.Errorf("redeeming a code twice = %v, want ErrOAuthRecordNotFound", err)
		}
	})

	t.Run("refuses an expired or unknown authorization code", func(t *testing.T) {
		client := newClient(t, "expiredcode")
		user := newUser(t, ports)
		hash := []byte("expired-code-" + user.Username)

		if err := ports.CreateAuthorizationCode(ctx, hash, core.OAuthAuthorizationCode{
			ClientID: client.ID, UserID: user.ID, RedirectURI: client.RedirectURIs[0],
			Scope: core.OAuthScopeMCP, ExpiresAt: time.Now().Add(-time.Minute),
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := ports.ConsumeAuthorizationCode(ctx, hash); !errors.Is(err, core.ErrOAuthRecordNotFound) {
			t.Errorf("redeeming an expired code = %v, want ErrOAuthRecordNotFound", err)
		}
		if _, err := ports.ConsumeAuthorizationCode(ctx, []byte("no-such-code")); !errors.Is(err, core.ErrOAuthRecordNotFound) {
			t.Errorf("redeeming an unknown code = %v, want ErrOAuthRecordNotFound", err)
		}
	})

	// An access token lookup is what every MCP request runs, so it has to
	// return the identity and the audience the token was bound to.
	t.Run("resolves an access token to its grant", func(t *testing.T) {
		client := newClient(t, "access")
		user := newUser(t, ports)
		grant := newGrant(client, user, "access")
		access := []byte("access-" + user.Username)

		if err := ports.CreateTokenPair(ctx, core.OAuthTokenPair{
			Grant: grant, AccessTokenHash: access, RefreshTokenHash: []byte("refresh-" + user.Username),
			AccessExpiresAt:  time.Now().Add(core.OAuthAccessTokenLifespan),
			RefreshExpiresAt: time.Now().Add(core.OAuthRefreshTokenLifespan),
		}); err != nil {
			t.Fatal(err)
		}

		found, err := ports.GetGrantByAccessToken(ctx, access)
		if err != nil {
			t.Fatal(err)
		}
		if found.UserID != user.ID || found.ClientID != client.ID || found.FamilyID != grant.FamilyID {
			t.Errorf("grant = %#v, want the grant the pair was issued under", found)
		}
		if found.Audience != grant.Audience || found.Scope != grant.Scope {
			t.Errorf("grant = %#v, want the audience and scope bound to the token", found)
		}
		// The username is what the request is authenticated as.
		if found.Username != user.Username {
			t.Errorf("Username = %q, want %q", found.Username, user.Username)
		}
	})

	t.Run("refuses an expired or unknown access token", func(t *testing.T) {
		client := newClient(t, "expiredaccess")
		user := newUser(t, ports)
		access := []byte("expired-access-" + user.Username)

		if err := ports.CreateTokenPair(ctx, core.OAuthTokenPair{
			Grant: newGrant(client, user, "expiredaccess"), AccessTokenHash: access,
			RefreshTokenHash: []byte("expired-refresh-" + user.Username),
			AccessExpiresAt:  time.Now().Add(-time.Minute),
			RefreshExpiresAt: time.Now().Add(time.Hour),
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := ports.GetGrantByAccessToken(ctx, access); !errors.Is(err, core.ErrOAuthRecordNotFound) {
			t.Errorf("an expired access token = %v, want ErrOAuthRecordNotFound", err)
		}
		if _, err := ports.GetGrantByAccessToken(ctx, []byte("no-such-token")); !errors.Is(err, core.ErrOAuthRecordNotFound) {
			t.Errorf("an unknown access token = %v, want ErrOAuthRecordNotFound", err)
		}
	})

	// Rotation: redeeming a refresh token returns the grant to reissue under
	// and retires both halves of the pair it replaces.
	t.Run("rotates a refresh token and retires the pair it replaces", func(t *testing.T) {
		client := newClient(t, "rotate")
		user := newUser(t, ports)
		grant := newGrant(client, user, "rotate")
		access := []byte("rotate-access-" + user.Username)
		refresh := []byte("rotate-refresh-" + user.Username)

		if err := ports.CreateTokenPair(ctx, core.OAuthTokenPair{
			Grant: grant, AccessTokenHash: access, RefreshTokenHash: refresh,
			AccessExpiresAt:  time.Now().Add(core.OAuthAccessTokenLifespan),
			RefreshExpiresAt: time.Now().Add(core.OAuthRefreshTokenLifespan),
		}); err != nil {
			t.Fatal(err)
		}

		rotated, err := ports.ConsumeRefreshToken(ctx, refresh)
		if err != nil {
			t.Fatal(err)
		}
		if rotated.UserID != user.ID || rotated.FamilyID != grant.FamilyID {
			t.Errorf("rotated grant = %#v, want the same authorization and family", rotated)
		}
		// The access token chained to it stops working, so a rotation cannot
		// leave a live token behind for whoever held the old pair.
		if _, err := ports.GetGrantByAccessToken(ctx, access); !errors.Is(err, core.ErrOAuthRecordNotFound) {
			t.Errorf("the old access token still resolves after rotation: %v", err)
		}
	})

	// Presenting a rotated refresh token means two parties hold it, so the
	// store reports the reuse and revokes everything descended from that one
	// authorization rather than only the token presented.
	t.Run("treats refresh reuse as theft and revokes the whole family", func(t *testing.T) {
		client := newClient(t, "reuse")
		user := newUser(t, ports)
		grant := newGrant(client, user, "reuse")

		first := []byte("reuse-refresh-1-" + user.Username)
		if err := ports.CreateTokenPair(ctx, core.OAuthTokenPair{
			Grant: grant, AccessTokenHash: []byte("reuse-access-1-" + user.Username), RefreshTokenHash: first,
			AccessExpiresAt:  time.Now().Add(core.OAuthAccessTokenLifespan),
			RefreshExpiresAt: time.Now().Add(core.OAuthRefreshTokenLifespan),
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := ports.ConsumeRefreshToken(ctx, first); err != nil {
			t.Fatal(err)
		}

		// The legitimate replacement, issued under the same family.
		second := []byte("reuse-refresh-2-" + user.Username)
		secondAccess := []byte("reuse-access-2-" + user.Username)
		if err := ports.CreateTokenPair(ctx, core.OAuthTokenPair{
			Grant: grant, AccessTokenHash: secondAccess, RefreshTokenHash: second,
			AccessExpiresAt:  time.Now().Add(core.OAuthAccessTokenLifespan),
			RefreshExpiresAt: time.Now().Add(core.OAuthRefreshTokenLifespan),
		}); err != nil {
			t.Fatal(err)
		}

		// Now the thief presents the token that was already rotated.
		if _, err := ports.ConsumeRefreshToken(ctx, first); !errors.Is(err, core.ErrOAuthRefreshReuse) {
			t.Fatalf("presenting a rotated refresh token = %v, want ErrOAuthRefreshReuse", err)
		}

		// The legitimate client is logged out too — that is the point.
		if _, err := ports.GetGrantByAccessToken(ctx, secondAccess); !errors.Is(err, core.ErrOAuthRecordNotFound) {
			t.Errorf("the family's live access token survived the reuse: %v", err)
		}
		if _, err := ports.ConsumeRefreshToken(ctx, second); !errors.Is(err, core.ErrOAuthRefreshReuse) {
			t.Errorf("the family's live refresh token survived the reuse: %v", err)
		}
	})

	t.Run("refuses an expired or unknown refresh token", func(t *testing.T) {
		client := newClient(t, "expiredrefresh")
		user := newUser(t, ports)
		refresh := []byte("expired-refresh-token-" + user.Username)

		if err := ports.CreateTokenPair(ctx, core.OAuthTokenPair{
			Grant:           newGrant(client, user, "expiredrefresh"),
			AccessTokenHash: []byte("expired-pair-access-" + user.Username), RefreshTokenHash: refresh,
			AccessExpiresAt:  time.Now().Add(time.Hour),
			RefreshExpiresAt: time.Now().Add(-time.Minute),
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := ports.ConsumeRefreshToken(ctx, refresh); !errors.Is(err, core.ErrOAuthRecordNotFound) {
			t.Errorf("an expired refresh token = %v, want ErrOAuthRecordNotFound", err)
		}
		if _, err := ports.ConsumeRefreshToken(ctx, []byte("no-such-refresh")); !errors.Is(err, core.ErrOAuthRecordNotFound) {
			t.Errorf("an unknown refresh token = %v, want ErrOAuthRecordNotFound", err)
		}
	})

	// Explicit revocation is what a sign-out has to reach, so it must take
	// the paired access token with it and must not fail on a token that is
	// already gone.
	t.Run("revokes tokens and tolerates revoking twice", func(t *testing.T) {
		client := newClient(t, "revoke")
		user := newUser(t, ports)
		access := []byte("revoke-access-" + user.Username)
		refresh := []byte("revoke-refresh-" + user.Username)

		if err := ports.CreateTokenPair(ctx, core.OAuthTokenPair{
			Grant: newGrant(client, user, "revoke"), AccessTokenHash: access, RefreshTokenHash: refresh,
			AccessExpiresAt:  time.Now().Add(core.OAuthAccessTokenLifespan),
			RefreshExpiresAt: time.Now().Add(core.OAuthRefreshTokenLifespan),
		}); err != nil {
			t.Fatal(err)
		}

		if err := ports.RevokeRefreshToken(ctx, refresh); err != nil {
			t.Fatal(err)
		}
		if _, err := ports.GetGrantByAccessToken(ctx, access); !errors.Is(err, core.ErrOAuthRecordNotFound) {
			t.Errorf("revoking the refresh token left its access token live: %v", err)
		}
		// Already revoked, so presenting it now reads as reuse.
		if _, err := ports.ConsumeRefreshToken(ctx, refresh); !errors.Is(err, core.ErrOAuthRefreshReuse) {
			t.Errorf("using a revoked refresh token = %v, want ErrOAuthRefreshReuse", err)
		}

		// RFC 7009: revoking an unknown token is not an error.
		if err := ports.RevokeRefreshToken(ctx, []byte("no-such-refresh")); err != nil {
			t.Errorf("revoking an unknown refresh token = %v, want nil", err)
		}
		if err := ports.RevokeAccessToken(ctx, []byte("no-such-access")); err != nil {
			t.Errorf("revoking an unknown access token = %v, want nil", err)
		}
		if err := ports.RevokeAccessToken(ctx, access); err != nil {
			t.Errorf("revoking an already-gone access token = %v, want nil", err)
		}
	})
}
