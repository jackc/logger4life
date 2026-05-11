package backend

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupOAuthTestServer wires up the OAuth + MCP routes against the test
// database, mirroring the production wiring in server.go.
func setupOAuthTestServer(t *testing.T, canonicalURL string) (*httptest.Server, *oauthProvider) {
	t.Helper()

	pool, err := pgxpool.New(context.Background(), "postgres://postgres:postgres@localhost:5432/logger4life_test")
	require.NoError(t, err)

	t.Cleanup(func() {
		ctx := context.Background()
		pool.Exec(ctx, "DELETE FROM oauth_pkce_sessions")
		pool.Exec(ctx, "DELETE FROM oauth_authorize_codes")
		pool.Exec(ctx, "DELETE FROM oauth_access_tokens")
		pool.Exec(ctx, "DELETE FROM oauth_refresh_tokens")
		pool.Exec(ctx, "DELETE FROM oauth_clients")
		pool.Close()
	})

	oauth, err := newOAuthProvider(pool, canonicalURL)
	require.NoError(t, err)
	mcpSrv := newMCPServer(pool, oauth)

	r := chi.NewRouter()
	r.Use(loadSession(pool))
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

	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv, oauth
}

func TestProtectedResourceMetadata(t *testing.T) {
	srv, _ := setupOAuthTestServer(t, "http://localhost:9999")

	resp, err := http.Get(srv.URL + "/.well-known/oauth-protected-resource")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Equal(t, "http://localhost:9999", body["resource"])
	assert.Equal(t, []any{"http://localhost:9999"}, body["authorization_servers"])
	assert.Equal(t, []any{"mcp"}, body["scopes_supported"])
	assert.Equal(t, "*", resp.Header.Get("Access-Control-Allow-Origin"))
}

func TestAuthorizationServerMetadata(t *testing.T) {
	srv, _ := setupOAuthTestServer(t, "http://localhost:9999")

	resp, err := http.Get(srv.URL + "/.well-known/oauth-authorization-server")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Equal(t, "http://localhost:9999", body["issuer"])
	assert.Equal(t, "http://localhost:9999/oauth/authorize", body["authorization_endpoint"])
	assert.Equal(t, "http://localhost:9999/oauth/token", body["token_endpoint"])
	assert.Equal(t, "http://localhost:9999/oauth/register", body["registration_endpoint"])
	assert.Equal(t, []any{"S256"}, body["code_challenge_methods_supported"])
	grants := body["grant_types_supported"].([]any)
	assert.Contains(t, grants, "authorization_code")
	assert.Contains(t, grants, "refresh_token")
}

func TestDynamicClientRegistration(t *testing.T) {
	srv, _ := setupOAuthTestServer(t, "http://localhost:9999")

	body := strings.NewReader(`{"redirect_uris":["https://claude.ai/api/mcp/auth_callback"],"client_name":"Claude"}`)
	resp, err := http.Post(srv.URL+"/oauth/register", "application/json", body)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	var got map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	assert.NotEmpty(t, got["client_id"])
	assert.Equal(t, "none", got["token_endpoint_auth_method"])
	assert.Equal(t, []any{"https://claude.ai/api/mcp/auth_callback"}, got["redirect_uris"])
	assert.Equal(t, "Claude", got["client_name"])
}

func TestDynamicClientRegistrationRejectsBadRedirect(t *testing.T) {
	srv, _ := setupOAuthTestServer(t, "http://localhost:9999")

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
	srv, _ := setupOAuthTestServer(t, "http://localhost:9999")

	// No Authorization header
	resp, err := http.Post(srv.URL+"/mcp", "application/json", strings.NewReader(`{}`))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	wwwAuth := resp.Header.Get("WWW-Authenticate")
	assert.Contains(t, wwwAuth, `Bearer resource_metadata="http://localhost:9999/.well-known/oauth-protected-resource"`)
}

func TestMCPRejectsInvalidToken(t *testing.T) {
	srv, _ := setupOAuthTestServer(t, "http://localhost:9999")

	req, err := http.NewRequest(http.MethodPost, srv.URL+"/mcp", strings.NewReader(`{}`))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer not_a_real_token")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	wwwAuth := resp.Header.Get("WWW-Authenticate")
	assert.Contains(t, wwwAuth, "invalid_token")
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
