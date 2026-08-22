package core

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"github.com/jackc/logger4life/backend/domain"
)

type fakeOAuthStore struct {
	client        OAuthClient
	clientErr     error
	createdClient OAuthClient

	codeHash   []byte
	codeRecord OAuthAuthorizationCode

	consumedCodeHash []byte
	consumedCode     OAuthAuthorizationCode
	consumeCodeErr   error

	pair OAuthTokenPair

	accessHash []byte
	grant      OAuthGrant
	grantErr   error

	refreshHash []byte
	refreshErr  error

	revoked []string
}

func (s *fakeOAuthStore) CreateOAuthClient(_ context.Context, c OAuthClient) error {
	s.createdClient = c
	return nil
}

func (s *fakeOAuthStore) GetOAuthClient(_ context.Context, _ string) (OAuthClient, error) {
	return s.client, s.clientErr
}

func (s *fakeOAuthStore) CreateAuthorizationCode(_ context.Context, hash []byte, c OAuthAuthorizationCode) error {
	s.codeHash = append([]byte(nil), hash...)
	s.codeRecord = c
	return nil
}

func (s *fakeOAuthStore) ConsumeAuthorizationCode(_ context.Context, hash []byte) (OAuthAuthorizationCode, error) {
	s.consumedCodeHash = append([]byte(nil), hash...)
	return s.consumedCode, s.consumeCodeErr
}

func (s *fakeOAuthStore) CreateTokenPair(_ context.Context, pair OAuthTokenPair) error {
	s.pair = pair
	return nil
}

func (s *fakeOAuthStore) GetGrantByAccessToken(_ context.Context, hash []byte) (OAuthGrant, error) {
	s.accessHash = append([]byte(nil), hash...)
	return s.grant, s.grantErr
}

func (s *fakeOAuthStore) ConsumeRefreshToken(_ context.Context, hash []byte) (OAuthGrant, error) {
	s.refreshHash = append([]byte(nil), hash...)
	return s.grant, s.refreshErr
}

func (s *fakeOAuthStore) RevokeAccessToken(_ context.Context, _ []byte) error {
	s.revoked = append(s.revoked, "access")
	return nil
}

func (s *fakeOAuthStore) RevokeRefreshToken(_ context.Context, _ []byte) error {
	s.revoked = append(s.revoked, "refresh")
	return nil
}

const testIssuer = "https://logger.example.com"

func registeredClient() OAuthClient {
	return OAuthClient{ID: "client-1", RedirectURIs: []string{"http://localhost/cb"}, ClientName: "Test"}
}

func pkcePair() (verifier, challenge string) {
	verifier = "test-verifier-with-enough-entropy-for-pkce-min-43-chars"
	sum := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(sum[:])
}

func validAuthorizationParams() OAuthAuthorizationParams {
	_, challenge := pkcePair()
	return OAuthAuthorizationParams{
		ResponseType:        "code",
		ClientID:            "client-1",
		RedirectURI:         "http://localhost/cb",
		Scope:               OAuthScopeMCP,
		State:               "deadbeef-state",
		CodeChallenge:       challenge,
		CodeChallengeMethod: "S256",
	}
}

func oauthErrorFrom(t *testing.T, err error) *OAuthError {
	t.Helper()
	var oauthErr *OAuthError
	if !errors.As(err, &oauthErr) {
		t.Fatalf("error = %v, want an *OAuthError", err)
	}
	return oauthErr
}

func TestRegisterOAuthClientRejectsUnusableRedirects(t *testing.T) {
	for name, uris := range map[string][]string{
		"none":       nil,
		"plain http": {"http://evil.example.com/cb"},
		"fragment":   {"https://example.com/cb#frag"},
	} {
		t.Run(name, func(t *testing.T) {
			store := &fakeOAuthStore{}
			app := New(Config{OAuth: store, OAuthIssuer: testIssuer})

			_, err := RegisterOAuthClient.Call(context.Background(), app, RegisterOAuthClientParams{RedirectURIs: uris})
			if got := oauthErrorFrom(t, err).Code; got != "invalid_redirect_uri" {
				t.Fatalf("code = %q, want invalid_redirect_uri", got)
			}
			if store.createdClient.ID != "" {
				t.Fatal("rejected registration must not reach the store")
			}
		})
	}
}

func TestPrepareOAuthAuthorizationRejections(t *testing.T) {
	_, challenge := pkcePair()
	cases := []struct {
		name         string
		clientErr    error
		mutate       func(*OAuthAuthorizationParams)
		wantCode     string
		redirectable bool
	}{
		{name: "unknown client", clientErr: ErrOAuthRecordNotFound, mutate: func(*OAuthAuthorizationParams) {}, wantCode: "invalid_client"},
		{name: "unregistered redirect", mutate: func(p *OAuthAuthorizationParams) { p.RedirectURI = "http://localhost/other" }, wantCode: "invalid_redirect_uri"},
		{name: "wrong response type", mutate: func(p *OAuthAuthorizationParams) { p.ResponseType = "token" }, wantCode: "unsupported_response_type", redirectable: true},
		{name: "missing pkce", mutate: func(p *OAuthAuthorizationParams) { p.CodeChallenge = "" }, wantCode: "invalid_request", redirectable: true},
		{name: "plain pkce", mutate: func(p *OAuthAuthorizationParams) { p.CodeChallengeMethod = "plain" }, wantCode: "invalid_request", redirectable: true},
		{name: "short state", mutate: func(p *OAuthAuthorizationParams) { p.State = "short" }, wantCode: "invalid_request", redirectable: true},
		{name: "unsupported scope", mutate: func(p *OAuthAuthorizationParams) { p.Scope = "mcp admin" }, wantCode: "invalid_scope", redirectable: true},
		{name: "foreign audience", mutate: func(p *OAuthAuthorizationParams) { p.Resource = "https://other.example.com" }, wantCode: "invalid_target", redirectable: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			store := &fakeOAuthStore{client: registeredClient(), clientErr: c.clientErr}
			app := New(Config{OAuth: store, OAuthIssuer: testIssuer})
			params := validAuthorizationParams()
			params.CodeChallenge = challenge
			c.mutate(&params)

			_, err := PrepareOAuthAuthorization.Call(context.Background(), app, params)
			oauthErr := oauthErrorFrom(t, err)
			if oauthErr.Code != c.wantCode {
				t.Fatalf("code = %q, want %q", oauthErr.Code, c.wantCode)
			}
			// A failure found before the redirect URI is known to belong to
			// the client must never be redirected to that URI.
			if oauthErr.Redirectable != c.redirectable {
				t.Fatalf("redirectable = %v, want %v", oauthErr.Redirectable, c.redirectable)
			}
		})
	}
}

func TestPrepareOAuthAuthorizationDefaults(t *testing.T) {
	store := &fakeOAuthStore{client: registeredClient()}
	app := New(Config{OAuth: store, OAuthIssuer: testIssuer + "/"})
	params := validAuthorizationParams()
	params.Scope = ""

	req, err := PrepareOAuthAuthorization.Call(context.Background(), app, params)
	if err != nil {
		t.Fatal(err)
	}
	if req.Scope != OAuthScopeMCP {
		t.Fatalf("scope = %q, want %q", req.Scope, OAuthScopeMCP)
	}
	if req.Audience != testIssuer {
		t.Fatalf("audience = %q, want the issuer %q", req.Audience, testIssuer)
	}
	if req.Client.ClientName != "Test" {
		t.Fatalf("client = %+v, want the registered client", req.Client)
	}
}

func TestCreateOAuthAuthorizationCodeStoresOnlyTheHash(t *testing.T) {
	store := &fakeOAuthStore{client: registeredClient()}
	app := New(Config{OAuth: store, OAuthIssuer: testIssuer})
	ctx := WithUserID(context.Background(), "user-1")

	result, err := CreateOAuthAuthorizationCode.Call(ctx, app, validAuthorizationParams())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(store.codeHash, domain.HashToken(result.Code)) {
		t.Fatal("persisted code hash does not match the issued code")
	}
	if bytes.Contains(store.codeHash, []byte(result.Code)) {
		t.Fatal("plaintext code reached the store")
	}
	if store.codeRecord.UserID != "user-1" {
		t.Fatalf("code user = %q, want the caller", store.codeRecord.UserID)
	}
	if store.codeRecord.Audience != testIssuer {
		t.Fatalf("code audience = %q, want the issuer", store.codeRecord.Audience)
	}
	if !store.codeRecord.ExpiresAt.After(time.Now()) {
		t.Fatal("issued code is already expired")
	}
}

func TestCreateOAuthAuthorizationCodeRequiresUser(t *testing.T) {
	store := &fakeOAuthStore{client: registeredClient()}
	app := New(Config{OAuth: store, OAuthIssuer: testIssuer})

	_, err := CreateOAuthAuthorizationCode.Call(context.Background(), app, validAuthorizationParams())
	if !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("err = %v, want ErrUnauthenticated", err)
	}
	if store.codeHash != nil {
		t.Fatal("anonymous request must not mint a code")
	}
}

func storedCode() OAuthAuthorizationCode {
	_, challenge := pkcePair()
	return OAuthAuthorizationCode{
		ClientID:            "client-1",
		UserID:              "user-1",
		RedirectURI:         "http://localhost/cb",
		Scope:               OAuthScopeMCP,
		Audience:            testIssuer,
		CodeChallenge:       challenge,
		CodeChallengeMethod: "S256",
		ExpiresAt:           time.Now().Add(time.Minute),
	}
}

func TestExchangeOAuthCodeRejections(t *testing.T) {
	verifier, _ := pkcePair()
	valid := ExchangeOAuthCodeParams{
		ClientID: "client-1", Code: "l4l_ac_x", RedirectURI: "http://localhost/cb",
		CodeVerifier: verifier, Resource: testIssuer,
	}
	cases := []struct {
		name       string
		consumeErr error
		mutate     func(*ExchangeOAuthCodeParams)
		wantCode   string
	}{
		{name: "missing verifier", mutate: func(p *ExchangeOAuthCodeParams) { p.CodeVerifier = "" }, wantCode: "invalid_request"},
		{name: "unknown code", consumeErr: ErrOAuthRecordNotFound, mutate: func(*ExchangeOAuthCodeParams) {}, wantCode: "invalid_grant"},
		{name: "other client", mutate: func(p *ExchangeOAuthCodeParams) { p.ClientID = "client-2" }, wantCode: "invalid_grant"},
		{name: "other redirect", mutate: func(p *ExchangeOAuthCodeParams) { p.RedirectURI = "http://localhost/other" }, wantCode: "invalid_grant"},
		{name: "wrong verifier", mutate: func(p *ExchangeOAuthCodeParams) {
			p.CodeVerifier = "a-different-verifier-that-is-still-long-enough-43ch"
		}, wantCode: "invalid_grant"},
		{name: "other audience", mutate: func(p *ExchangeOAuthCodeParams) { p.Resource = "https://other.example.com" }, wantCode: "invalid_target"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			store := &fakeOAuthStore{consumedCode: storedCode(), consumeCodeErr: c.consumeErr}
			app := New(Config{OAuth: store, OAuthIssuer: testIssuer})
			params := valid
			c.mutate(&params)

			_, err := ExchangeOAuthCode.Call(context.Background(), app, params)
			if got := oauthErrorFrom(t, err).Code; got != c.wantCode {
				t.Fatalf("code = %q, want %q", got, c.wantCode)
			}
			if store.pair.AccessTokenHash != nil {
				t.Fatal("rejected exchange must not issue tokens")
			}
		})
	}
}

func TestExchangeOAuthCodeIssuesHashedPair(t *testing.T) {
	verifier, _ := pkcePair()
	store := &fakeOAuthStore{consumedCode: storedCode()}
	app := New(Config{OAuth: store, OAuthIssuer: testIssuer})

	tokens, err := ExchangeOAuthCode.Call(context.Background(), app, ExchangeOAuthCodeParams{
		ClientID: "client-1", Code: "l4l_ac_x", RedirectURI: "http://localhost/cb", CodeVerifier: verifier,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(store.consumedCodeHash, domain.HashToken("l4l_ac_x")) {
		t.Fatal("exchange looked up something other than the code hash")
	}
	if !bytes.Equal(store.pair.AccessTokenHash, domain.HashToken(tokens.AccessToken)) ||
		!bytes.Equal(store.pair.RefreshTokenHash, domain.HashToken(tokens.RefreshToken)) {
		t.Fatal("persisted hashes do not match the issued tokens")
	}
	if tokens.AccessToken == tokens.RefreshToken {
		t.Fatal("access and refresh tokens must differ")
	}
	if store.pair.Grant.FamilyID == "" {
		t.Fatal("a fresh authorization must start a token family")
	}
	if store.pair.Grant.UserID != "user-1" || store.pair.Grant.Audience != testIssuer {
		t.Fatalf("grant = %+v, want the code's user and audience", store.pair.Grant)
	}
	if !store.pair.AccessExpiresAt.Before(store.pair.RefreshExpiresAt) {
		t.Fatal("access token must expire before its refresh token")
	}
}

func TestRefreshOAuthTokenKeepsFamilyAndHidesReuse(t *testing.T) {
	grant := OAuthGrant{ClientID: "client-1", UserID: "user-1", Scope: OAuthScopeMCP, Audience: testIssuer, FamilyID: "family-1"}
	store := &fakeOAuthStore{grant: grant}
	app := New(Config{OAuth: store, OAuthIssuer: testIssuer})

	tokens, err := RefreshOAuthToken.Call(context.Background(), app, RefreshOAuthTokenParams{
		ClientID: "client-1", RefreshToken: "l4l_rt_x",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(store.refreshHash, domain.HashToken("l4l_rt_x")) {
		t.Fatal("refresh looked up something other than the token hash")
	}
	if store.pair.Grant.FamilyID != "family-1" {
		t.Fatalf("family = %q, want it carried across rotation", store.pair.Grant.FamilyID)
	}
	if !bytes.Equal(store.pair.AccessTokenHash, domain.HashToken(tokens.AccessToken)) {
		t.Fatal("persisted hash does not match the rotated access token")
	}

	// Reuse of an already-rotated token is reported to the caller as the same
	// generic failure as any other bad token, but keeps its cause so adapters
	// can log the detection.
	reuse := &fakeOAuthStore{refreshErr: ErrOAuthRefreshReuse}
	reuseApp := New(Config{OAuth: reuse, OAuthIssuer: testIssuer})
	_, err = RefreshOAuthToken.Call(context.Background(), reuseApp, RefreshOAuthTokenParams{
		ClientID: "client-1", RefreshToken: "l4l_rt_x",
	})
	oauthErr := oauthErrorFrom(t, err)
	if oauthErr.Code != "invalid_grant" {
		t.Fatalf("code = %q, want invalid_grant", oauthErr.Code)
	}
	if !errors.Is(err, ErrOAuthRefreshReuse) {
		t.Fatal("reuse detection must survive as the error's cause")
	}

	missing := &fakeOAuthStore{refreshErr: ErrOAuthRecordNotFound}
	_, err = RefreshOAuthToken.Call(context.Background(), New(Config{OAuth: missing, OAuthIssuer: testIssuer}),
		RefreshOAuthTokenParams{ClientID: "client-1", RefreshToken: "l4l_rt_x"})
	if got := oauthErrorFrom(t, err); got.Description != oauthErr.Description {
		t.Fatalf("unknown token description = %q, want it indistinguishable from reuse", got.Description)
	}
}

func TestRefreshOAuthTokenRejectsOtherClient(t *testing.T) {
	store := &fakeOAuthStore{grant: OAuthGrant{ClientID: "client-1", Audience: testIssuer}}
	app := New(Config{OAuth: store, OAuthIssuer: testIssuer})

	_, err := RefreshOAuthToken.Call(context.Background(), app, RefreshOAuthTokenParams{
		ClientID: "client-2", RefreshToken: "l4l_rt_x",
	})
	if got := oauthErrorFrom(t, err).Code; got != "invalid_grant" {
		t.Fatalf("code = %q, want invalid_grant", got)
	}
	if store.pair.AccessTokenHash != nil {
		t.Fatal("a refresh token must not be usable by another client")
	}
}

func TestRevokeOAuthTokenTriesBothStores(t *testing.T) {
	store := &fakeOAuthStore{}
	app := New(Config{OAuth: store, OAuthIssuer: testIssuer})

	if _, err := RevokeOAuthToken.Call(context.Background(), app, RevokeOAuthTokenParams{Token: "l4l_at_x"}); err != nil {
		t.Fatal(err)
	}
	if len(store.revoked) != 2 || store.revoked[0] != "access" {
		t.Fatalf("revoked = %v, want access first then refresh", store.revoked)
	}

	hinted := &fakeOAuthStore{}
	if _, err := RevokeOAuthToken.Call(context.Background(), New(Config{OAuth: hinted, OAuthIssuer: testIssuer}),
		RevokeOAuthTokenParams{Token: "l4l_rt_x", TokenTypeHint: "refresh_token"}); err != nil {
		t.Fatal(err)
	}
	if len(hinted.revoked) != 2 || hinted.revoked[0] != "refresh" {
		t.Fatalf("revoked = %v, want the hinted type first", hinted.revoked)
	}

	empty := &fakeOAuthStore{}
	if _, err := RevokeOAuthToken.Call(context.Background(), New(Config{OAuth: empty, OAuthIssuer: testIssuer}),
		RevokeOAuthTokenParams{}); err != nil {
		t.Fatal(err)
	}
	if len(empty.revoked) != 0 {
		t.Fatal("an empty token must not reach the store")
	}
}

func TestAuthenticateOAuthToken(t *testing.T) {
	store := &fakeOAuthStore{grant: OAuthGrant{UserID: "user-1", Username: "sam", Audience: testIssuer + "/"}}
	app := New(Config{OAuth: store, OAuthIssuer: testIssuer})

	user, err := AuthenticateOAuthToken.Call(context.Background(), app, AuthenticateOAuthTokenParams{Token: "l4l_at_x"})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(store.accessHash, domain.HashToken("l4l_at_x")) {
		t.Fatal("lookup used something other than the token hash")
	}
	if user.ID != "user-1" || user.Username != "sam" {
		t.Fatalf("user = %+v, want the grant's user", user)
	}

	foreign := &fakeOAuthStore{grant: OAuthGrant{UserID: "user-1", Audience: "https://other.example.com"}}
	_, err = AuthenticateOAuthToken.Call(context.Background(), New(Config{OAuth: foreign, OAuthIssuer: testIssuer}),
		AuthenticateOAuthTokenParams{Token: "l4l_at_x"})
	if !errors.Is(err, ErrOAuthTokenAudienceMismatch) {
		t.Fatalf("err = %v, want ErrOAuthTokenAudienceMismatch", err)
	}

	missing := &fakeOAuthStore{grantErr: ErrOAuthRecordNotFound}
	_, err = AuthenticateOAuthToken.Call(context.Background(), New(Config{OAuth: missing, OAuthIssuer: testIssuer}),
		AuthenticateOAuthTokenParams{Token: "l4l_at_x"})
	if !errors.Is(err, ErrOAuthInvalidToken) {
		t.Fatalf("err = %v, want ErrOAuthInvalidToken", err)
	}

	_, err = AuthenticateOAuthToken.Call(context.Background(), app, AuthenticateOAuthTokenParams{})
	if !errors.Is(err, ErrOAuthInvalidToken) {
		t.Fatalf("empty token err = %v, want ErrOAuthInvalidToken", err)
	}
}
