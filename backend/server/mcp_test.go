package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/jackc/logger4life/backend/core"
	"github.com/jackc/logger4life/backend/domain"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mcpUserSQLExecutor struct {
	userIDs []string
	queries []string
}

func (e *mcpUserSQLExecutor) ExecuteUserSQL(_ context.Context, userID, query string) (core.UserSQLResult, error) {
	e.userIDs = append(e.userIDs, userID)
	e.queries = append(e.queries, query)
	value := query
	return core.UserSQLResult{
		Columns: []core.UserSQLColumn{{Name: "query", DataType: "text"}},
		Rows:    [][]*string{{&value}}, RowCount: 1,
	}, nil
}

type mcpSavedQueryStore struct{ saved core.SavedQuery }

func (s mcpSavedQueryStore) ListSavedQueries(context.Context, string) ([]core.SavedQuery, error) {
	return []core.SavedQuery{s.saved}, nil
}
func (s mcpSavedQueryStore) GetSavedQueryByName(_ context.Context, _, name string) (core.SavedQuery, error) {
	if name != s.saved.Name {
		return core.SavedQuery{}, core.ErrSavedQueryNotFound
	}
	return s.saved, nil
}
func (mcpSavedQueryStore) CreateSavedQuery(context.Context, string, string, string, string) (core.SavedQuery, error) {
	return core.SavedQuery{}, errors.New("not implemented")
}
func (mcpSavedQueryStore) UpdateSavedQuery(context.Context, string, string, string, string) (core.SavedQuery, error) {
	return core.SavedQuery{}, errors.New("not implemented")
}
func (mcpSavedQueryStore) DeleteSavedQuery(context.Context, string, string) error {
	return errors.New("not implemented")
}

func TestMCPSQLToolsUseSharedCoreAction(t *testing.T) {
	executor := &mcpUserSQLExecutor{}
	actions := []string{}
	middleware := func(next core.Handler) core.Handler {
		return func(ctx context.Context, invocation core.Invocation) (any, error) {
			actions = append(actions, invocation.Action.Name())
			return next(ctx, invocation)
		}
	}
	app := core.New(core.Config{
		UserSQL: executor,
		SavedQueries: mcpSavedQueryStore{saved: core.SavedQuery{
			Name: "saved", QueryText: "SELECT name FROM logs",
		}},
		Middleware: []core.Middleware{middleware},
	})
	mcpServer := newMCPServer(app, nil)
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverCtx := context.WithValue(context.Background(), userContextKey, &AuthUser{ID: "user-1", Username: "alice"})
	serverSession, err := mcpServer.server.Connect(serverCtx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer serverSession.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0.1"}, nil)
	clientSession, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer clientSession.Close()

	direct, err := clientSession.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "run_sql", Arguments: map[string]any{"query": "  SELECT 1  "},
	})
	if err != nil {
		t.Fatal(err)
	}
	if direct.StructuredContent.(map[string]any)["row_count"] != float64(1) {
		t.Fatalf("run_sql result = %#v", direct.StructuredContent)
	}
	if _, err := clientSession.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "run_saved_query", Arguments: map[string]any{"name": "saved"},
	}); err != nil {
		t.Fatal(err)
	}

	if len(executor.queries) != 2 || executor.queries[0] != "SELECT 1" || executor.queries[1] != "SELECT name FROM logs" {
		t.Fatalf("executor queries = %#v", executor.queries)
	}
	if executor.userIDs[0] != "user-1" || executor.userIDs[1] != "user-1" {
		t.Fatalf("executor user IDs = %#v", executor.userIDs)
	}
	wantActions := []string{"execute_user_sql", "get_saved_query", "execute_user_sql"}
	if len(actions) != len(wantActions) {
		t.Fatalf("actions = %#v", actions)
	}
	for i := range wantActions {
		if actions[i] != wantActions[i] {
			t.Fatalf("actions = %#v", actions)
		}
	}
}

func TestMCPToolErrorOnlyExposesExplicitlySafeErrors(t *testing.T) {
	safe := mcpToolError(context.Background(), &core.UserSQLFailure{Kind: core.UserSQLRejected, Message: "invalid SQL query"})
	if safe.Error() != "invalid SQL query" {
		t.Fatalf("safe error = %q", safe)
	}
	internal := mcpToolError(context.Background(), errors.New("database host is secret.internal"))
	if internal.Error() != "internal error" {
		t.Fatalf("internal error = %q", internal)
	}
}

type mcpOAuthStore struct{ core.OAuthStore }

func (mcpOAuthStore) GetGrantByAccessToken(_ context.Context, hash []byte) (core.OAuthGrant, error) {
	for _, userID := range []string{"alice", "bob"} {
		if bytes.Equal(hash, domain.HashToken(userID+"-token")) {
			return core.OAuthGrant{UserID: userID, Username: userID, Audience: "https://logs.example.com", Scope: core.OAuthScopeMCP}, nil
		}
	}
	return core.OAuthGrant{}, core.ErrOAuthRecordNotFound
}

func newMCPHTTPTestHandler(executor core.UserSQLExecutor) http.Handler {
	app := core.New(core.Config{
		OAuth: mcpOAuthStore{}, OAuthIssuer: "https://logs.example.com", UserSQL: executor,
		SavedQueries: mcpSavedQueryStore{saved: core.SavedQuery{Name: "saved", QueryText: "SELECT 1"}},
	})
	server := newMCPServer(app, newOAuthProvider(app, "https://logs.example.com"))
	return server.requireBearerToken()(server.handler)
}

func newMCPHTTPRequest(t *testing.T, version, method string, params map[string]any) *http.Request {
	t.Helper()
	if params == nil {
		params = map[string]any{}
	}
	if version == "2026-07-28" {
		params["_meta"] = map[string]any{
			"io.modelcontextprotocol/protocolVersion":    version,
			"io.modelcontextprotocol/clientCapabilities": map[string]any{},
			"io.modelcontextprotocol/clientInfo":         map[string]any{"name": "test", "version": "0.1"},
		}
	}
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "https://logs.example.com/mcp", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer alice-token")
	req.Header.Set("MCP-Protocol-Version", version)
	if version == "2026-07-28" {
		req.Header.Set("Mcp-Method", method)
		if name, ok := params["name"].(string); ok {
			req.Header.Set("Mcp-Name", name)
		}
	}
	return req
}

func serveMCPJSON(t *testing.T, handler http.Handler, req *http.Request) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body), "status %d: %s", w.Code, w.Body.String())
	assert.Empty(t, w.Header().Get("Mcp-Session-Id"))
	return w, body
}

func TestMCPHTTPProtocolCompatibility(t *testing.T) {
	for _, version := range []string{"2025-06-18", "2025-11-25", "2026-07-28"} {
		t.Run(version, func(t *testing.T) {
			executor := &mcpUserSQLExecutor{}
			handler := newMCPHTTPTestHandler(executor)
			if version == "2026-07-28" {
				w, body := serveMCPJSON(t, handler, newMCPHTTPRequest(t, version, "server/discover", nil))
				require.Equal(t, http.StatusOK, w.Code, "%v", body)
				result := body["result"].(map[string]any)
				assert.Contains(t, result["supportedVersions"], version)
				assert.Equal(t, "complete", result["resultType"])
			} else {
				w, body := serveMCPJSON(t, handler, newMCPHTTPRequest(t, version, "initialize", map[string]any{
					"protocolVersion": version, "capabilities": map[string]any{},
					"clientInfo": map[string]any{"name": "legacy-test", "version": "0.1"},
				}))
				require.Equal(t, http.StatusOK, w.Code, "%v", body)
				assert.Equal(t, version, body["result"].(map[string]any)["protocolVersion"])
			}

			w, body := serveMCPJSON(t, handler, newMCPHTTPRequest(t, version, "tools/list", nil))
			require.Equal(t, http.StatusOK, w.Code, "%v", body)
			result := body["result"].(map[string]any)
			if version == "2026-07-28" {
				assert.Equal(t, "complete", result["resultType"])
				assert.Equal(t, "public", result["cacheScope"])
				assert.Equal(t, float64(0), result["ttlMs"])
			}
			tools := result["tools"].([]any)
			require.Len(t, tools, 5)
			names := make([]string, 0, len(tools))
			for _, item := range tools {
				tool := item.(map[string]any)
				names = append(names, tool["name"].(string))
				assert.NotEmpty(t, tool["title"])
				assert.NotNil(t, tool["inputSchema"])
				assert.NotNil(t, tool["outputSchema"])
				annotations := tool["annotations"].(map[string]any)
				assert.Equal(t, true, annotations["readOnlyHint"])
				assert.Equal(t, false, annotations["destructiveHint"])
				assert.Equal(t, true, annotations["idempotentHint"])
				assert.Equal(t, false, annotations["openWorldHint"])
			}
			assert.True(t, slices.IsSorted(names), "tool order: %v", names)

			for _, userID := range []string{"alice", "bob"} {
				req := newMCPHTTPRequest(t, version, "tools/call", map[string]any{
					"name": "run_sql", "arguments": map[string]any{"query": "SELECT 1"},
				})
				req.Header.Set("Authorization", "bEaReR "+userID+"-token")
				// A stale or fabricated session ID must not bind the request to
				// another user or require the client to reinitialize.
				req.Header.Set("Mcp-Session-Id", "same-session")
				w, body = serveMCPJSON(t, handler, req)
				require.Equal(t, http.StatusOK, w.Code, "%v", body)
				result = body["result"].(map[string]any)
				assert.NotEqual(t, true, result["isError"], "%v", result)
				structured := result["structuredContent"].(map[string]any)
				assert.Equal(t, float64(1), structured["row_count"])
				content := result["content"].([]any)[0].(map[string]any)
				var textResult map[string]any
				require.NoError(t, json.Unmarshal([]byte(content["text"].(string)), &textResult))
				assert.Equal(t, structured, textResult)
			}
			assert.Equal(t, []string{"alice", "bob"}, executor.userIDs)

			w, body = serveMCPJSON(t, handler, newMCPHTTPRequest(t, version, "tools/call", map[string]any{
				"name": "run_sql", "arguments": map[string]any{"query": " "},
			}))
			require.Equal(t, http.StatusOK, w.Code, "%v", body)
			assert.Equal(t, true, body["result"].(map[string]any)["isError"])
			assert.Contains(t, w.Body.String(), "query is required")

			_, body = serveMCPJSON(t, handler, newMCPHTTPRequest(t, version, "tools/call", map[string]any{
				"name": "unknown", "arguments": map[string]any{},
			}))
			assert.Equal(t, float64(-32602), body["error"].(map[string]any)["code"])
		})
	}
}

func TestMCPHTTPRejectsMismatchedHeaders(t *testing.T) {
	handler := newMCPHTTPTestHandler(&mcpUserSQLExecutor{})
	for _, header := range []string{"Mcp-Method", "Mcp-Name", "MCP-Protocol-Version"} {
		t.Run(header, func(t *testing.T) {
			req := newMCPHTTPRequest(t, "2026-07-28", "tools/call", map[string]any{
				"name": "run_sql", "arguments": map[string]any{"query": "SELECT 1"},
			})
			value := "wrong"
			if header == "MCP-Protocol-Version" {
				value = "2025-11-25"
			}
			req.Header.Set(header, value)
			w, body := serveMCPJSON(t, handler, req)
			assert.Equal(t, http.StatusBadRequest, w.Code)
			assert.Equal(t, float64(-32020), body["error"].(map[string]any)["code"])
		})
	}
}

func TestMCPHTTPOriginAndAuthentication(t *testing.T) {
	handler := newMCPHTTPTestHandler(&mcpUserSQLExecutor{})
	for _, method := range []string{http.MethodPost, http.MethodGet, http.MethodDelete} {
		for _, origin := range []string{"", "https://logs.example.com", "https://evil.example", "null"} {
			t.Run(method+"/"+origin, func(t *testing.T) {
				req := newMCPHTTPRequest(t, "2026-07-28", "tools/list", nil)
				req.Method = method
				if origin != "" {
					req.Header.Set("Origin", origin)
				}
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, req)
				switch {
				case origin != "" && origin != "https://logs.example.com":
					assert.Equal(t, http.StatusForbidden, w.Code)
				case method != http.MethodPost:
					assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
					assert.Equal(t, "POST", w.Header().Get("Allow"))
				default:
					assert.Equal(t, http.StatusOK, w.Code)
				}
			})
		}
		for _, authorization := range []string{"", "Basic abc", "Bearer ", "Bearer invalid"} {
			req := newMCPHTTPRequest(t, "2026-07-28", "tools/list", nil)
			req.Method = method
			req.Header.Set("Authorization", authorization)
			// A cookie user must never stand in for an OAuth bearer token.
			req = req.WithContext(context.WithValue(req.Context(), userContextKey, &AuthUser{ID: "cookie-user"}))
			w, _ := serveMCPJSON(t, handler, req)
			assert.Equal(t, http.StatusUnauthorized, w.Code)
			assert.Contains(t, w.Header().Get("WWW-Authenticate"), `scope="mcp"`)
			if authorization == "" || authorization == "Basic abc" {
				assert.NotContains(t, w.Header().Get("WWW-Authenticate"), "invalid_token")
			} else {
				assert.Contains(t, w.Header().Get("WWW-Authenticate"), "invalid_token")
			}
		}
	}
}

type mcpScopeOAuthStore struct {
	core.OAuthStore
	grant core.OAuthGrant
	err   error
}

func (s mcpScopeOAuthStore) GetGrantByAccessToken(context.Context, []byte) (core.OAuthGrant, error) {
	return s.grant, s.err
}

func TestMCPHTTPRequiresGrantedScope(t *testing.T) {
	const issuer = "https://logs.example.com"
	for _, tc := range []struct {
		name, scope, audience string
		storeErr              error
		wantStatus            int
		wantError             string
	}{
		{name: "empty", audience: issuer, wantStatus: http.StatusForbidden, wantError: "insufficient_scope"},
		{name: "whitespace", scope: " \t\n ", audience: issuer, wantStatus: http.StatusForbidden, wantError: "insufficient_scope"},
		{name: "unrelated", scope: "admin", audience: issuer, wantStatus: http.StatusForbidden, wantError: "insufficient_scope"},
		{name: "substring", scope: "mcp:read", audience: issuer, wantStatus: http.StatusForbidden, wantError: "insufficient_scope"},
		{name: "wrong case", scope: "MCP", audience: issuer, wantStatus: http.StatusForbidden, wantError: "insufficient_scope"},
		{name: "mcp", scope: "mcp", audience: issuer, wantStatus: http.StatusNoContent},
		{name: "scope set", scope: "other mcp extra", audience: issuer, wantStatus: http.StatusNoContent},
		{name: "wrong audience takes precedence", audience: "https://other.example.com", wantStatus: http.StatusUnauthorized, wantError: "invalid_token"},
		{name: "invalid token takes precedence", audience: issuer, storeErr: core.ErrOAuthRecordNotFound, wantStatus: http.StatusUnauthorized, wantError: "invalid_token"},
		{name: "store failure is sanitized", audience: issuer, storeErr: errors.New("secret database failure"), wantStatus: http.StatusUnauthorized, wantError: "invalid_token"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := core.New(core.Config{
				OAuth: mcpScopeOAuthStore{
					grant: core.OAuthGrant{UserID: "alice", Username: "alice", Scope: tc.scope, Audience: tc.audience},
					err:   tc.storeErr,
				},
				OAuthIssuer: issuer,
			})
			server := newMCPServer(app, newOAuthProvider(app, issuer))
			called := false
			handler := server.requireBearerToken()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				require.Equal(t, &AuthUser{ID: "alice", Username: "alice"}, userFromContext(r.Context()))
				w.WriteHeader(http.StatusNoContent)
			}))
			for _, method := range []string{http.MethodPost, http.MethodGet, http.MethodDelete} {
				t.Run(method, func(t *testing.T) {
					called = false
					req := newMCPHTTPRequest(t, "2026-07-28", "tools/list", nil)
					req.Method = method
					// A browser session must not substitute for the token's missing scope.
					req = req.WithContext(context.WithValue(req.Context(), userContextKey, &AuthUser{ID: "cookie-user"}))
					w := httptest.NewRecorder()
					handler.ServeHTTP(w, req)
					require.Equal(t, tc.wantStatus, w.Code, w.Body.String())
					assert.Equal(t, tc.wantError == "", called, "resource handler must run only with granted MCP scope")
					if tc.wantError == "" {
						assert.Empty(t, w.Header().Get("WWW-Authenticate"))
						return
					}
					challenge := w.Header().Get("WWW-Authenticate")
					assert.Contains(t, challenge, `Bearer resource_metadata="`+issuer+`/.well-known/oauth-protected-resource"`)
					assert.Contains(t, challenge, `scope="mcp"`)
					assert.Contains(t, challenge, `error="`+tc.wantError+`"`)
					assert.NotContains(t, challenge+w.Body.String(), "secret")
					if tc.wantError == "insufficient_scope" {
						assert.NotContains(t, challenge, "invalid_token")
						var body map[string]string
						require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
						assert.Equal(t, "insufficient_scope", body["error"])
					}
				})
			}
		})
	}
}

type cancellingMCPExecutor struct{ started, cancelled chan struct{} }

func (e *cancellingMCPExecutor) ExecuteUserSQL(ctx context.Context, _, _ string) (core.UserSQLResult, error) {
	close(e.started)
	<-ctx.Done()
	close(e.cancelled)
	return core.UserSQLResult{}, ctx.Err()
}

func TestMCPHTTPDisconnectCancelsTool(t *testing.T) {
	executor := &cancellingMCPExecutor{started: make(chan struct{}), cancelled: make(chan struct{})}
	handler := newMCPHTTPTestHandler(executor)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req := newMCPHTTPRequest(t, "2026-07-28", "tools/call", map[string]any{
		"name": "run_sql", "arguments": map[string]any{"query": "SELECT 1"},
	}).WithContext(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		handler.ServeHTTP(httptest.NewRecorder(), req)
	}()
	select {
	case <-executor.started:
	case <-ctx.Done():
		t.Fatal("tool did not start")
	}
	cancel() // net/http cancels this context when the client disconnects.
	select {
	case <-executor.cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("request cancellation did not reach the SQL executor")
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("HTTP handler did not return after cancellation")
	}
}
