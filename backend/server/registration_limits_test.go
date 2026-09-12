package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/logger4life/backend/core"
	"github.com/stretchr/testify/require"
)

type registrationStore struct {
	core.OAuthStore
	created int
}

func (s *registrationStore) CreateOAuthClient(context.Context, core.OAuthClient) error {
	s.created++
	return nil
}

func TestRegistrationInputLimits(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"valid", `{"redirect_uris":["https://example.com/cb"],"extension":true}`, 201},
		{"large body", `{"extension":"` + strings.Repeat("x", 33<<10) + `"}`, 413},
		{"large trailing whitespace", `{"redirect_uris":["https://example.com/cb"]}` + strings.Repeat(" ", 33<<10), 413},
		{"second object", `{"redirect_uris":["https://example.com/cb"]}{}`, 400},
		{"trailing garbage", `{"redirect_uris":["https://example.com/cb"]}x`, 400},
		{"large name", `{"redirect_uris":["https://example.com/cb"],"client_name":"` + strings.Repeat("x", 257) + `"}`, 400},
		{"many redirects", `{"redirect_uris":[` + strings.Repeat(`"https://example.com/cb",`, 10) + `"https://example.com/cb"]}`, 400},
		{"large URI", `{"redirect_uris":["https://example.com/` + strings.Repeat("x", 2048) + `"]}`, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &registrationStore{}
			p := newOAuthProvider(core.New(core.Config{OAuth: store}), "https://example.com")
			r := httptest.NewRequest(http.MethodPost, "/oauth/register", strings.NewReader(tc.body))
			r.ContentLength = -1 // Limits must also work without Content-Length.
			w := httptest.NewRecorder()
			p.handleDynamicClientRegistration()(w, r)
			require.Equal(t, tc.status, w.Code, w.Body.String())
			if tc.status != 201 {
				require.Zero(t, store.created)
			}
		})
	}
}

func TestRegistrationThrottles(t *testing.T) {
	store := &registrationStore{}
	p := newOAuthProvider(core.New(core.Config{OAuth: store}), "https://example.com")
	call := func(peer string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/oauth/register", strings.NewReader(`{"redirect_uris":["https://example.com/cb"]}`))
		r.RemoteAddr = peer
		r.Header.Set("X-Forwarded-For", "203.0.113.99")
		w := httptest.NewRecorder()
		p.handleDynamicClientRegistration()(w, r)
		return w
	}
	for range 5 {
		require.Equal(t, 201, call("192.0.2.1:1234").Code)
	}
	w := call("192.0.2.1:5678")
	require.Equal(t, 429, w.Code)
	require.NotEmpty(t, w.Header().Get("Retry-After"))
	for range 5 {
		require.Equal(t, 201, call("192.0.2.2:1234").Code)
	}
	require.Equal(t, 429, call("192.0.2.3:1234").Code)
	require.Equal(t, 10, store.created)
}

func TestRequestClientIP(t *testing.T) {
	trusted, err := parseTrustedProxies("127.0.0.1/32, ::1/128")
	require.NoError(t, err)
	for _, tc := range []struct{ peer, forwarded, want string }{
		{"192.0.2.1:1", "203.0.113.1", "192.0.2.1"},
		{"127.0.0.1:1", "203.0.113.1, 192.0.2.1", "192.0.2.1"},
		{"[::1]:1", "203.0.113.1, 127.0.0.1", "203.0.113.1"},
		{"127.0.0.1:1", "203.0.113.1, invalid", "127.0.0.1"},
		{"[::ffff:192.0.2.1]:1", "203.0.113.1", "192.0.2.1"},
	} {
		r := httptest.NewRequest(http.MethodPost, "/", nil)
		r.RemoteAddr = tc.peer
		r.Header.Set("X-Forwarded-For", tc.forwarded)
		require.Equal(t, tc.want, requestClientIP(r, trusted))
	}
}
