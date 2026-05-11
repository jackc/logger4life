package backend

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupOAuthTestServer wires the OAuth + MCP routes against the test
// database. The httptest server is started after the OAuth provider is
// constructed using the eventual server URL, so audience binding works.
func setupOAuthTestServer(t *testing.T) (*httptest.Server, *oauthProvider, *pgxpool.Pool) {
	t.Helper()

	pool, err := pgxpool.New(context.Background(), "postgres://postgres:postgres@localhost:5432/logger4life_test")
	require.NoError(t, err)

	t.Cleanup(func() {
		ctx := context.Background()
		pool.Exec(ctx, "DELETE FROM oauth_refresh_tokens")
		pool.Exec(ctx, "DELETE FROM oauth_access_tokens")
		pool.Exec(ctx, "DELETE FROM oauth_authorization_codes")
		pool.Exec(ctx, "DELETE FROM oauth_clients")
		pool.Exec(ctx, "DELETE FROM sessions")
		pool.Exec(ctx, "DELETE FROM logs")
		pool.Exec(ctx, "DELETE FROM users")
		pool.Close()
	})

	srv := httptest.NewUnstartedServer(nil)
	srv.Start() // starts on a real port; URL is now known
	t.Cleanup(srv.Close)

	oauth := newOAuthProvider(pool, srv.URL)
	mcpSrv := newMCPServer(pool, oauth)

	r := chi.NewRouter()
	r.Use(loadSession(pool))
	r.Post("/api/register", handleRegister(pool, true))
	r.Post("/api/login", handleLogin(pool))
	r.Get("/.well-known/oauth-protected-resource", oauth.handleProtectedResourceMetadata())
	r.Get("/.well-known/oauth-authorization-server", oauth.handleAuthorizationServerMetadata())
	r.Post("/oauth/register", oauth.handleDynamicClientRegistration())
	r.Get("/oauth/authorize", oauth.handleAuthorize())
	r.Post("/oauth/authorize", oauth.handleAuthorize())
	r.Post("/oauth/token", oauth.handleToken())
	r.Group(func(r chi.Router) {
		r.Use(mcpSrv.requireBearerToken())
		r.Mount("/mcp", mcpSrv.handler)
	})
	srv.Config.Handler = r
	return srv, oauth, pool
}

// pkceParams generates a fresh verifier + S256 challenge pair.
func pkceParams(t *testing.T) (verifier, challenge string) {
	t.Helper()
	verifier = "test-verifier-with-enough-entropy-for-pkce-min-43-chars"
	sum := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(sum[:])
	return
}

func TestProtectedResourceMetadata(t *testing.T) {
	srv, _, _ := setupOAuthTestServer(t)

	resp, err := http.Get(srv.URL + "/.well-known/oauth-protected-resource")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Equal(t, srv.URL, body["resource"])
	assert.Equal(t, []any{srv.URL}, body["authorization_servers"])
	assert.Equal(t, []any{"mcp"}, body["scopes_supported"])
	assert.Equal(t, "*", resp.Header.Get("Access-Control-Allow-Origin"))
}

func TestAuthorizationServerMetadata(t *testing.T) {
	srv, _, _ := setupOAuthTestServer(t)

	resp, err := http.Get(srv.URL + "/.well-known/oauth-authorization-server")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Equal(t, srv.URL, body["issuer"])
	assert.Equal(t, srv.URL+"/oauth/authorize", body["authorization_endpoint"])
	assert.Equal(t, srv.URL+"/oauth/token", body["token_endpoint"])
	assert.Equal(t, srv.URL+"/oauth/register", body["registration_endpoint"])
	assert.Equal(t, []any{"S256"}, body["code_challenge_methods_supported"])
}

func TestDynamicClientRegistration(t *testing.T) {
	srv, _, _ := setupOAuthTestServer(t)

	body := strings.NewReader(`{"redirect_uris":["https://claude.ai/api/mcp/auth_callback"],"client_name":"Claude"}`)
	resp, err := http.Post(srv.URL+"/oauth/register", "application/json", body)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	var got map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	assert.NotEmpty(t, got["client_id"])
	assert.Equal(t, "none", got["token_endpoint_auth_method"])
	assert.Equal(t, "Claude", got["client_name"])
}

func TestDynamicClientRegistrationRejectsBadRedirect(t *testing.T) {
	srv, _, _ := setupOAuthTestServer(t)

	body := strings.NewReader(`{"redirect_uris":["http://evil.example.com/cb"]}`)
	resp, err := http.Post(srv.URL+"/oauth/register", "application/json", body)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

	var got map[string]string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	assert.Equal(t, "invalid_redirect_uri", got["error"])
}

func TestMCPRequiresBearerToken(t *testing.T) {
	srv, _, _ := setupOAuthTestServer(t)

	resp, err := http.Post(srv.URL+"/mcp", "application/json", strings.NewReader(`{}`))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	wwwAuth := resp.Header.Get("WWW-Authenticate")
	assert.Contains(t, wwwAuth, `Bearer resource_metadata="`+srv.URL+`/.well-known/oauth-protected-resource"`)
}

func TestMCPRejectsInvalidToken(t *testing.T) {
	srv, _, _ := setupOAuthTestServer(t)

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/mcp", strings.NewReader(`{}`))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer not_a_real_token")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("WWW-Authenticate"), "invalid_token")
}

func TestVerifyPKCE(t *testing.T) {
	verifier, challenge := pkceParams(t)
	assert.True(t, verifyPKCE(challenge, "S256", verifier))
	assert.False(t, verifyPKCE(challenge, "S256", "wrong-verifier-but-still-long-enough-43-chars-yes"))
	assert.False(t, verifyPKCE(challenge, "plain", verifier))
	assert.False(t, verifyPKCE(challenge, "S256", "too-short"))
}

func TestSameCanonicalURL(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"http://localhost:4001", "http://localhost:4001", true},
		{"http://localhost:4001/", "http://localhost:4001", true},
		{"HTTPS://EXAMPLE.com", "https://example.com", true},
		{"http://localhost:4001", "http://localhost:4002", false},
		{"http://example.com", "https://example.com", false},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, sameCanonicalURL(c.a, c.b),
			"sameCanonicalURL(%q, %q)", c.a, c.b)
	}
}

// TestOAuthEndToEnd exercises register → DCR → authorize (with consent) →
// token → /mcp invocation in one shot, validating the full hand-rolled
// pipeline including PKCE verification, code consumption, audience
// binding, code-replay rejection, and refresh-token rotation.
func TestOAuthEndToEnd(t *testing.T) {
	srv, _, _ := setupOAuthTestServer(t)

	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	client := &http.Client{Jar: jar}

	// Register a user; this also drops a session cookie into the jar.
	resp, err := client.Post(srv.URL+"/api/register", "application/json",
		strings.NewReader(`{"username":"e2e_user","password":"password123"}`))
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	// DCR.
	resp, err = client.Post(srv.URL+"/oauth/register", "application/json",
		strings.NewReader(`{"redirect_uris":["http://localhost/cb"]}`))
	require.NoError(t, err)
	var dcr map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&dcr))
	resp.Body.Close()
	clientID := dcr["client_id"].(string)

	verifier, challenge := pkceParams(t)
	authValues := url.Values{}
	authValues.Set("response_type", "code")
	authValues.Set("client_id", clientID)
	authValues.Set("redirect_uri", "http://localhost/cb")
	authValues.Set("code_challenge", challenge)
	authValues.Set("code_challenge_method", "S256")
	authValues.Set("scope", "mcp")
	authValues.Set("state", "deadbeef-state")
	authValues.Set("resource", srv.URL)
	authValues.Set("approve", "true")

	// Don't follow the redirect; we want to read its Location header.
	noFollow := &http.Client{
		Jar: jar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err = noFollow.PostForm(srv.URL+"/oauth/authorize", authValues)
	require.NoError(t, err)
	require.Equal(t, http.StatusSeeOther, resp.StatusCode)
	loc, err := url.Parse(resp.Header.Get("Location"))
	resp.Body.Close()
	require.NoError(t, err)
	require.Equal(t, "deadbeef-state", loc.Query().Get("state"))
	code := loc.Query().Get("code")
	require.NotEmpty(t, code, "expected code in redirect; got %s", resp.Header.Get("Location"))

	// Exchange code for tokens.
	tokValues := url.Values{}
	tokValues.Set("grant_type", "authorization_code")
	tokValues.Set("client_id", clientID)
	tokValues.Set("code", code)
	tokValues.Set("redirect_uri", "http://localhost/cb")
	tokValues.Set("code_verifier", verifier)
	tokValues.Set("resource", srv.URL)
	resp, err = http.PostForm(srv.URL+"/oauth/token", tokValues)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var tok map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&tok))
	resp.Body.Close()
	access := tok["access_token"].(string)
	refresh := tok["refresh_token"].(string)
	require.NotEmpty(t, access)
	require.NotEmpty(t, refresh)
	assert.Equal(t, "Bearer", tok["token_type"])

	// /mcp initialize with the access token should now succeed (200).
	initReq, _ := http.NewRequest(http.MethodPost, srv.URL+"/mcp", strings.NewReader(
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"0.1"}}}`))
	initReq.Header.Set("Authorization", "Bearer "+access)
	initReq.Header.Set("Content-Type", "application/json")
	initReq.Header.Set("Accept", "application/json, text/event-stream")
	initResp, err := http.DefaultClient.Do(initReq)
	require.NoError(t, err)
	defer initResp.Body.Close()
	assert.Equal(t, http.StatusOK, initResp.StatusCode)

	// Replaying the consumed authorization code should fail (defense in
	// depth even though PKCE alone would catch this).
	replayResp, err := http.PostForm(srv.URL+"/oauth/token", tokValues)
	require.NoError(t, err)
	defer replayResp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, replayResp.StatusCode)

	// Refresh-token grant produces a fresh access token.
	refreshValues := url.Values{}
	refreshValues.Set("grant_type", "refresh_token")
	refreshValues.Set("client_id", clientID)
	refreshValues.Set("refresh_token", refresh)
	refreshValues.Set("resource", srv.URL)
	refreshResp, err := http.PostForm(srv.URL+"/oauth/token", refreshValues)
	require.NoError(t, err)
	defer refreshResp.Body.Close()
	require.Equal(t, http.StatusOK, refreshResp.StatusCode)
	var refreshed map[string]any
	require.NoError(t, json.NewDecoder(refreshResp.Body).Decode(&refreshed))
	assert.NotEqual(t, access, refreshed["access_token"], "refreshed token should differ")
}

// TestRefreshTokenReuseRevokesFamily verifies OAuth 2.1 BCP §4.14.2: when
// a refresh token that has already been rotated is presented again, the
// entire token family is revoked — both the newly-issued refresh token
// and its associated access token become unusable.
func TestRefreshTokenReuseRevokesFamily(t *testing.T) {
	srv, _, _ := setupOAuthTestServer(t)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}

	resp, err := client.Post(srv.URL+"/api/register", "application/json",
		strings.NewReader(`{"username":"reuse_user","password":"password123"}`))
	require.NoError(t, err)
	resp.Body.Close()

	resp, err = client.Post(srv.URL+"/oauth/register", "application/json",
		strings.NewReader(`{"redirect_uris":["http://localhost/cb"]}`))
	require.NoError(t, err)
	var dcr map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&dcr))
	resp.Body.Close()
	clientID := dcr["client_id"].(string)

	verifier, challenge := pkceParams(t)
	noFollow := &http.Client{
		Jar:           jar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse },
	}
	v := url.Values{}
	v.Set("response_type", "code")
	v.Set("client_id", clientID)
	v.Set("redirect_uri", "http://localhost/cb")
	v.Set("code_challenge", challenge)
	v.Set("code_challenge_method", "S256")
	v.Set("scope", "mcp")
	v.Set("state", "reuse-test-state")
	v.Set("resource", srv.URL)
	v.Set("approve", "true")
	resp, err = noFollow.PostForm(srv.URL+"/oauth/authorize", v)
	require.NoError(t, err)
	loc, _ := url.Parse(resp.Header.Get("Location"))
	resp.Body.Close()
	code := loc.Query().Get("code")

	tokValues := url.Values{}
	tokValues.Set("grant_type", "authorization_code")
	tokValues.Set("client_id", clientID)
	tokValues.Set("code", code)
	tokValues.Set("redirect_uri", "http://localhost/cb")
	tokValues.Set("code_verifier", verifier)
	tokValues.Set("resource", srv.URL)
	resp, err = http.PostForm(srv.URL+"/oauth/token", tokValues)
	require.NoError(t, err)
	var tok map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&tok))
	resp.Body.Close()
	refresh1 := tok["refresh_token"].(string)

	// First refresh succeeds and rotates the refresh token.
	refreshValues := url.Values{}
	refreshValues.Set("grant_type", "refresh_token")
	refreshValues.Set("client_id", clientID)
	refreshValues.Set("refresh_token", refresh1)
	resp, err = http.PostForm(srv.URL+"/oauth/token", refreshValues)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var refreshed map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&refreshed))
	resp.Body.Close()
	refresh2 := refreshed["refresh_token"].(string)
	access2 := refreshed["access_token"].(string)

	// Presenting the original (now-revoked) refresh token again is the
	// reuse signal — should fail with invalid_grant.
	resp, err = http.PostForm(srv.URL+"/oauth/token", refreshValues)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)

	// As a side effect, the newly-rotated refresh token AND its access
	// token must also be revoked — the entire family is dead.
	familyRefresh := url.Values{}
	familyRefresh.Set("grant_type", "refresh_token")
	familyRefresh.Set("client_id", clientID)
	familyRefresh.Set("refresh_token", refresh2)
	resp, err = http.PostForm(srv.URL+"/oauth/token", familyRefresh)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode,
		"rotated refresh token must be revoked after reuse of its predecessor")

	// And the access token from the second pair should no longer authenticate.
	mcpReq, _ := http.NewRequest(http.MethodPost, srv.URL+"/mcp", strings.NewReader(
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"0.1"}}}`))
	mcpReq.Header.Set("Authorization", "Bearer "+access2)
	mcpReq.Header.Set("Content-Type", "application/json")
	mcpReq.Header.Set("Accept", "application/json, text/event-stream")
	mcpResp, err := http.DefaultClient.Do(mcpReq)
	require.NoError(t, err)
	defer mcpResp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, mcpResp.StatusCode,
		"access token in revoked family must be rejected by /mcp")
}

// TestAuthorizeRejectsAudienceMismatch verifies RFC 8707 enforcement: a
// resource parameter that doesn't match our canonical URL is rejected.
func TestAuthorizeRejectsAudienceMismatch(t *testing.T) {
	srv, _, _ := setupOAuthTestServer(t)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}

	resp, err := client.Post(srv.URL+"/api/register", "application/json",
		strings.NewReader(`{"username":"aud_user","password":"password123"}`))
	require.NoError(t, err)
	resp.Body.Close()

	resp, err = client.Post(srv.URL+"/oauth/register", "application/json",
		strings.NewReader(`{"redirect_uris":["http://localhost/cb"]}`))
	require.NoError(t, err)
	var dcr map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&dcr))
	resp.Body.Close()
	clientID := dcr["client_id"].(string)

	_, challenge := pkceParams(t)
	noFollow := &http.Client{
		Jar:           jar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse },
	}
	v := url.Values{}
	v.Set("response_type", "code")
	v.Set("client_id", clientID)
	v.Set("redirect_uri", "http://localhost/cb")
	v.Set("code_challenge", challenge)
	v.Set("code_challenge_method", "S256")
	v.Set("scope", "mcp")
	v.Set("state", "abcdefgh")
	v.Set("resource", "http://wrong.example.com")
	v.Set("approve", "true")
	resp, err = noFollow.PostForm(srv.URL+"/oauth/authorize", v)
	require.NoError(t, err)
	defer resp.Body.Close()
	loc, _ := url.Parse(resp.Header.Get("Location"))
	assert.Equal(t, "invalid_target", loc.Query().Get("error"))
	assert.Empty(t, loc.Query().Get("code"))
}
