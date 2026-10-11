package server

import (
	"context"
	"encoding/json/v2"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/logger4life/backend/core"
	"github.com/jackc/manifest/httpregistry"
	"github.com/stretchr/testify/require"
)

func TestHTTPInventoryAndInstalledPolicies(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "default", true: "features"}[enabled], func(t *testing.T) {
			cfg := DefaultConfig()
			if enabled {
				cfg.MCPCanonicalURL = "https://logs.example.com"
				cfg.WebAuthnRPID = "localhost"
				cfg.WebAuthnOrigin = "http://localhost"
			}
			inventory, err := HTTPInventory(cfg)
			require.NoError(t, err)
			routes := newHTTPRegistry(nil, func(context.Context) error { return nil }, cfg, slog.New(slog.DiscardHandler), nil)
			require.Equal(t, inventory, routes.Inventory())
			h := routes.Handler()
			bindings := map[string]string{}
			conditional := 0
			for _, e := range inventory.Endpoints {
				if e.When != nil {
					conditional++
					require.Equal(t, enabled, e.Enabled)
				}
				if e.Binding != nil {
					bindings[e.Method+" "+e.Path] = e.Binding.Action
				}
				if e.Enabled && !e.Metadata.Public {
					path := e.Path
					// Placeholder text is itself a nonempty segment; the authentication
					// gate must reject it before parameter validation or any operation.
					method := e.Method
					if method == "" {
						method = "POST"
					}
					req := httptest.NewRequest(method, path, nil)
					w := httptest.NewRecorder()
					h.ServeHTTP(w, req)
					require.Equal(t, 401, w.Code, "%s %s", method, path)
					require.NotEmpty(t, w.Header().Get("X-Request-Id"))
				}
				switch e.Path {
				case "/api/login":
					require.True(t, e.Metadata.Public)
					require.Nil(t, e.Binding)
				case "/api/register":
					require.True(t, e.Enabled)
					require.Nil(t, e.When)
				case "/mcp":
					require.Equal(t, "mount", e.Kind)
					require.Equal(t, "oauth-bearer", e.Metadata.Credentials)
					require.NotEmpty(t, e.Gaps)
				}
			}
			require.Equal(t, 15, conditional)
			require.Equal(t, map[string]string{"GET /api/logs": "list_logs", "GET /api/logs/{logID}": "get_log", "POST /api/logs/{logID}/entries": "create_log_entry"}, bindings)
			// Actual dispatch agrees with the declaration's feature state.
			for _, path := range []string{"/api/me/passkeys", "/mcp"} {
				w := httptest.NewRecorder()
				h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
				if enabled {
					require.Equal(t, 401, w.Code)
				} else {
					require.Equal(t, 404, w.Code)
				}
			}
			data, err := json.Marshal(inventory)
			require.NoError(t, err)
			require.NotContains(t, string(data), cfg.DatabaseURL)
		})
	}
}

func TestHTTPInventoryValidatesConfiguration(t *testing.T) {
	cfg := DefaultConfig()
	cfg.MCPCanonicalURL = "invalid"
	_, err := HTTPInventory(cfg)
	require.Error(t, err)
	cfg = DefaultConfig()
	cfg.MCPRequestsPerMinute = -1
	_, err = HTTPInventory(cfg)
	require.Error(t, err)
}

func TestEntryBindingStrictDecodingAndDispatch(t *testing.T) {
	srv := setupTestRouter(t)
	defer srv.Close()
	cookies := registerUser(t, srv.URL, "bindinguser")
	logID := createLogViaAPI(t, srv.URL, "binding log", cookies)
	url := srv.URL + "/api/logs/" + logID + "/entries"
	for _, tc := range []struct{ name, body string }{
		{"body-path-conflict", `{"log_id":"other"}`},
		{"unknown-field", `{"extra":true}`},
		{"case-alias", `{"Note":"hello"}`},
		{"duplicate-key", `{"note":"first","note":"last"}`},
		{"nested-duplicate", `{"fields":{"x":1,"x":2}}`},
		{"trailing-document", `{} {}`},
		{"null", `null`},
		{"array", `[]`},
		{"invalid-utf8", "{\"note\":\"\xff\"}"},
		{"invalid-surrogate", `{"note":"\ud800"}`},
		{"malformed", `broken`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest("POST", url, strings.NewReader(tc.body))
			require.NoError(t, err)
			for _, cookie := range cookies {
				req.AddCookie(cookie)
			}
			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()
			require.Equal(t, http.StatusBadRequest, resp.StatusCode)
			var body map[string]any
			require.NoError(t, json.UnmarshalRead(resp.Body, &body))
			require.Equal(t, "invalid request body", body["error"])
		})
	}
	resp, body := postJSON(url, map[string]any{"note": "hello", "fields": map[string]any{}}, cookies)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	require.Equal(t, logID, body["log_id"])
	require.Equal(t, "hello", body["note"])
	resp, entries := getJSONArray(url, cookies)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Len(t, entries, 1, "rejected requests must not create entries")
}

func TestHTTPBindingInputErrorsPrecedeDomainErrorPolicy(t *testing.T) {
	// A future query mapping on a read must not send parse errors through its
	// domain error writer (which could classify them as internal errors).
	for _, domainPolicy := range []httpregistry.ErrorWriter{
		{Name: "internal-error", Write: internalError},
		{Name: "log-operation-error", Write: writeLogOperationError},
	} {
		t.Run(domainPolicy.Name, func(t *testing.T) {
			routes := httpregistry.New(func(httpregistry.Spec[struct{}]) ([]httpregistry.Stage, error) { return nil, nil })
			routes.Handle(httpregistry.Spec[struct{}]{Method: "GET", Path: "/"}, bindHTTPAction(core.GetLog, nil, httpregistry.Binding{
				JSON: httpregistry.NoJSON, Status: http.StatusOK, Errors: domainPolicy,
				Inputs: []httpregistry.Input{{Source: "query", Name: "id", Field: "LogID"}},
			}))
			w := httptest.NewRecorder()
			routes.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/?id=a&id=b", nil))
			require.Equal(t, 400, w.Code)
			require.JSONEq(t, `{"error":"invalid request parameters"}`, w.Body.String())
		})
	}
	// Domain failures still reach the supplied writer unchanged.
	domainFailure := errors.New("domain failure")
	app := core.New(core.Config{Middleware: []core.Middleware{func(core.Handler) core.Handler {
		return func(context.Context, core.Invocation) (any, error) { return nil, domainFailure }
	}}})
	routes := httpregistry.New(func(httpregistry.Spec[struct{}]) ([]httpregistry.Stage, error) { return nil, nil })
	routes.Handle(httpregistry.Spec[struct{}]{Method: "GET", Path: "/"}, bindHTTPAction(core.ListLogs, app, httpregistry.Binding{
		JSON: httpregistry.NoJSON, Status: 200,
		Errors: httpregistry.ErrorWriter{Name: "domain", Write: func(w http.ResponseWriter, _ *http.Request, err error) {
			require.ErrorIs(t, err, domainFailure)
			w.WriteHeader(409)
		}},
	}))
	w := httptest.NewRecorder()
	routes.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	require.Equal(t, 409, w.Code)
}

func TestHTTPRegistryConfiguresOAuthForProductionAndInjectedProviders(t *testing.T) {
	for _, injected := range []bool{false, true} {
		t.Run(map[bool]string{false: "production", true: "injected"}[injected], func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.MCPCanonicalURL = "https://logs.example.com"
			cfg.OAuthRegistrationPerIP = 1
			cfg.OAuthRegistrationGlobal = 2
			cfg.TrustedProxyCIDRs = "127.0.0.1/32"
			store := &registrationStore{}
			app := core.New(core.Config{OAuth: store})
			var oauth *oauthProvider
			if injected {
				oauth = newOAuthProvider(app, cfg.MCPCanonicalURL)
			}
			handler := newHTTPRegistry(app, nil, cfg, slog.New(slog.DiscardHandler), oauth).Handler()
			call := func(clientIP string) *httptest.ResponseRecorder {
				req := httptest.NewRequest("POST", "/oauth/register", strings.NewReader(`{"redirect_uris":["https://example.com/cb"]}`))
				req.RemoteAddr = "127.0.0.1:1234"
				req.Header.Set("X-Forwarded-For", clientIP)
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, req)
				return w
			}
			for range 5 {
				require.Equal(t, 201, call("192.0.2.1").Code)
			}
			w := call("192.0.2.1")
			require.Equal(t, 429, w.Code)
			retry, err := strconv.Atoi(w.Header().Get("Retry-After"))
			require.NoError(t, err)
			require.Greater(t, retry, 50)
			require.LessOrEqual(t, retry, 60)
			// Same proxy, another verified client IP: a separate five-request bucket.
			for range 5 {
				require.Equal(t, 201, call("192.0.2.2").Code)
			}
			w = call("192.0.2.3")
			require.Equal(t, 429, w.Code)
			retry, err = strconv.Atoi(w.Header().Get("Retry-After"))
			require.NoError(t, err)
			require.Greater(t, retry, 20)
			require.LessOrEqual(t, retry, 30)
			require.Equal(t, 10, store.created)
		})
	}
}

func TestHTTPRegistryLeavesDisabledProtocolUninitialized(t *testing.T) {
	// An empty provider would be initialized by the enabled protocol factory.
	provider := &oauthProvider{}
	routes := newHTTPRegistry(nil, nil, DefaultConfig(), slog.New(slog.DiscardHandler), provider)
	require.Nil(t, provider.registrationIPs)
	require.Nil(t, provider.registrations)
	require.Nil(t, provider.metadataIPs)
	for _, entry := range routes.Inventory().Endpoints {
		if entry.When != nil && entry.When.Name == "mcp-enabled" {
			require.False(t, entry.Enabled)
		}
	}
	w := httptest.NewRecorder()
	routes.Handler().ServeHTTP(w, httptest.NewRequest("POST", "/mcp", nil))
	require.Equal(t, 404, w.Code)
}
