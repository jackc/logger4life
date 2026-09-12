package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/jackc/logger4life/backend/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// postOAuthConsent models a browser submitting the same-origin consent form.
func postOAuthConsent(client *http.Client, endpoint string, values url.Values) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(values.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", req.URL.Scheme+"://"+req.URL.Host)
	return client.Do(req)
}

type consentOAuthStore struct {
	core.OAuthStore
	lookups int
	codes   int
}

func (s *consentOAuthStore) GetOAuthClient(context.Context, string) (core.OAuthClient, error) {
	s.lookups++
	return core.OAuthClient{ID: "client", ClientName: "Client", RedirectURIs: []string{"https://client.example/cb"}}, nil
}

func (s *consentOAuthStore) CreateAuthorizationCode(context.Context, []byte, core.OAuthAuthorizationCode) error {
	s.codes++
	return nil
}

func TestOAuthConsentOrigin(t *testing.T) {
	const canonical = "https://logs.example.com"
	for _, tc := range []struct {
		name      string
		origins   []string
		fetchSite string
		allowed   bool
	}{
		{name: "same origin", origins: []string{canonical}, fetchSite: "same-origin", allowed: true},
		{name: "origin without fetch metadata", origins: []string{canonical}, allowed: true},
		{name: "sibling", origins: []string{"https://sibling.example.com"}, fetchSite: "same-site"},
		{name: "cross site", origins: []string{"https://attacker.test"}, fetchSite: "cross-site"},
		{name: "different scheme", origins: []string{"http://logs.example.com"}},
		{name: "different port", origins: []string{"https://logs.example.com:8443"}},
		{name: "origin with path", origins: []string{canonical + "/"}},
		{name: "missing"},
		{name: "empty", origins: []string{""}},
		{name: "null", origins: []string{"null"}},
		{name: "duplicate", origins: []string{canonical, canonical}},
		{name: "multiple", origins: []string{canonical, "https://attacker.test"}},
		{name: "joined", origins: []string{canonical + ", https://attacker.test"}},
		{name: "fetch metadata alone", fetchSite: "same-origin"},
		{name: "forged fetch metadata", origins: []string{"https://attacker.test"}, fetchSite: "same-origin"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, decision := range []string{"true", "false"} {
				t.Run(decision, func(t *testing.T) {
					store := &consentOAuthStore{}
					app := core.New(core.Config{OAuth: store, OAuthIssuer: canonical})
					provider := newOAuthProvider(app, canonical)
					values := consentTestValues(t)
					values.Set("approve", decision)
					req := httptest.NewRequest(http.MethodPost, "http://proxy.internal/oauth/authorize", strings.NewReader(values.Encode()))
					req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
					req.Header["Origin"] = tc.origins
					req.Header.Set("Sec-Fetch-Site", tc.fetchSite)
					// Neither an attacker-selected Host nor forwarding headers
					// may replace the configured public origin.
					req.Header.Set("X-Forwarded-Host", "attacker.test")
					req.Header.Set("Forwarded", "host=attacker.test;proto=https")
					req.Header.Set("Referer", canonical+"/oauth/authorize")
					req = req.WithContext(context.WithValue(req.Context(), userContextKey, &AuthUser{ID: "victim", Username: "victim"}))
					w := httptest.NewRecorder()
					provider.handleAuthorize().ServeHTTP(w, req)
					assertConsentFramingHeaders(t, w.Header())
					if !tc.allowed {
						assert.Equal(t, http.StatusForbidden, w.Code)
						assert.Empty(t, w.Header().Get("Location"))
						assert.Zero(t, store.lookups, "reject before core authorization")
						assert.Zero(t, store.codes)
						return
					}
					require.Equal(t, http.StatusSeeOther, w.Code)
					location, err := url.Parse(w.Header().Get("Location"))
					require.NoError(t, err)
					assert.Equal(t, canonical, location.Query().Get("iss"))
					if decision == "true" {
						assert.Equal(t, 1, store.codes)
						assert.NotEmpty(t, location.Query().Get("code"))
					} else {
						assert.Zero(t, store.codes)
						assert.Equal(t, "access_denied", location.Query().Get("error"))
					}
				})
			}
		})
	}
}

func consentTestValues(t *testing.T) url.Values {
	_, challenge := pkceParams(t)
	return url.Values{
		"response_type": {"code"}, "client_id": {"client"}, "redirect_uri": {"https://client.example/cb"},
		"state": {"attacker-chosen-state"}, "code_challenge": {challenge}, "code_challenge_method": {"S256"},
	}
}

func assertConsentFramingHeaders(t *testing.T, h http.Header) {
	t.Helper()
	assert.Equal(t, "frame-ancestors 'none'", h.Get("Content-Security-Policy"))
	assert.Equal(t, "DENY", h.Get("X-Frame-Options"))
}

func TestOAuthConsentGETAndFraming(t *testing.T) {
	for _, tc := range []struct {
		name          string
		authenticated bool
		valid         bool
		status        int
	}{
		{name: "consent", authenticated: true, valid: true, status: http.StatusOK},
		{name: "login", valid: true, status: http.StatusSeeOther},
		{name: "invalid request", authenticated: true, status: http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &consentOAuthStore{}
			provider := newOAuthProvider(core.New(core.Config{OAuth: store}), "https://logs.example.com")
			values := consentTestValues(t)
			if !tc.valid {
				values.Set("redirect_uri", "https://attacker.test/cb")
			}
			req := httptest.NewRequest(http.MethodGet, "https://logs.example.com/oauth/authorize?"+values.Encode(), nil)
			req.Header.Set("Origin", "https://client.example")
			req.Header.Set("Sec-Fetch-Site", "cross-site")
			if tc.authenticated {
				req = req.WithContext(context.WithValue(req.Context(), userContextKey, &AuthUser{ID: "victim", Username: "victim"}))
			}
			w := httptest.NewRecorder()
			provider.handleAuthorize().ServeHTTP(w, req)
			assert.Equal(t, tc.status, w.Code)
			assertConsentFramingHeaders(t, w.Header())
			assert.Zero(t, store.codes)
			if tc.status == http.StatusOK {
				assert.Contains(t, w.Body.String(), `action="/oauth/authorize"`)
			}
		})
	}
}
