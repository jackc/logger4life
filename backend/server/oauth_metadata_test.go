package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/logger4life/backend/core"
	"github.com/stretchr/testify/require"
)

type metadataResolverFunc func(context.Context, string) (core.OAuthClient, error)

func (f metadataResolverFunc) ResolveOAuthClient(ctx context.Context, id string) (core.OAuthClient, error) {
	return f(ctx, id)
}

func TestOAuthMetadataConsent(t *testing.T) {
	const id = "https://example.com/client.json"
	calls := 0
	client := core.OAuthClient{ID: id, ClientName: "<script>self-reported name</script>", RedirectURIs: []string{"https://client.example/cb"}}
	var fetchErr error
	app := core.New(core.Config{OAuth: &consentOAuthStore{}, OAuthIssuer: "https://logs.example.com", OAuthMetadata: metadataResolverFunc(func(context.Context, string) (core.OAuthClient, error) {
		calls++
		return client, fetchErr
	})})
	p := newOAuthProvider(app, "https://logs.example.com")
	values := consentTestValues(t)
	values.Set("client_id", id)
	get := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "/oauth/authorize?"+values.Encode(), nil)
		req = req.WithContext(context.WithValue(req.Context(), userContextKey, &AuthUser{ID: "user", Username: "User"}))
		w := httptest.NewRecorder()
		p.handleAuthorize()(w, req)
		return w
	}
	w := get()
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), "Client website: <strong>example.com</strong>")
	require.Contains(t, w.Body.String(), "&lt;script&gt;")
	require.NotContains(t, w.Body.String(), "<script>")
	require.Equal(t, "DENY", w.Header().Get("X-Frame-Options"))
	fetchErr = errors.New("internal network address")
	w = get()
	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Empty(t, w.Header().Get("Location"))
	require.Contains(t, w.Body.String(), "invalid_client")
	require.NotContains(t, w.Body.String(), "internal network")

	p.metadataIPs = newKeyedRateLimiter(1, 1)
	get()
	before := calls
	w = get()
	require.Equal(t, http.StatusTooManyRequests, w.Code)
	require.NotEmpty(t, w.Header().Get("Retry-After"))
	require.Equal(t, before, calls, "rate limit before fetching")
	// Forged approvals fail before spending metadata-fetch allowance.
	req := httptest.NewRequest("POST", "/oauth/authorize", strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://sibling.example.com")
	w = httptest.NewRecorder()
	p.handleAuthorize()(w, req)
	require.Equal(t, http.StatusForbidden, w.Code)
	require.Equal(t, before, calls)
}
