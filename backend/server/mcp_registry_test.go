package server

import (
	"context"
	"encoding/json/v2"
	"os"
	"testing"

	"github.com/jackc/logger4life/backend/core"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

func TestMCPInventoryMatchesServedCatalogAndPreviousContract(t *testing.T) {
	inventory := MCPInventory() // Does not require an application or OAuth provider.
	require.Equal(t, 1, inventory.SchemaVersion)
	require.Len(t, inventory.Tools, 5)
	tools := make([]mcp.Tool, 0, len(inventory.Tools))
	bindings := map[string]string{}
	for _, entry := range inventory.Tools {
		tools = append(tools, entry.Tool)
		require.True(t, entry.Metadata.RequiresUser)
		require.Equal(t, []string{"require-mcp-user"}, entry.Stages)
		require.NotEmpty(t, entry.Gaps)
		if entry.Binding != nil {
			bindings[entry.Tool.Name] = entry.Binding.Action
			require.Equal(t, "github.com/jackc/logger4life/backend/core", entry.Binding.Namespace)
			require.Equal(t, "safe-core-error", entry.Binding.Errors)
		} else {
			require.Equal(t, "custom-typed", entry.Kind)
		}
	}
	require.Equal(t, map[string]string{"list_logs": "list_logs_page", "list_saved_queries": "list_saved_queries_page"}, bindings)
	baseline, err := os.ReadFile("testdata/mcp-tools.json")
	require.NoError(t, err)
	described, err := json.Marshal(tools)
	require.NoError(t, err)
	require.JSONEq(t, string(baseline), string(described))
	handler := newMCPHTTPTestHandler(&mcpUserSQLExecutor{})
	_, body := serveMCPJSON(t, handler, newMCPHTTPRequest(t, "2026-07-28", "tools/list", nil))
	served, err := json.Marshal(body["result"].(map[string]any)["tools"])
	require.NoError(t, err)
	require.JSONEq(t, string(baseline), string(served))
}

func TestMCPRegistryRequiresUserAcrossAllTools(t *testing.T) {
	server := newMCPRegistry(nil).Server()
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	ss, err := server.Connect(context.Background(), serverTransport, nil)
	require.NoError(t, err)
	defer ss.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	cs, err := client.Connect(context.Background(), clientTransport, nil)
	require.NoError(t, err)
	defer cs.Close()
	for name, args := range map[string]any{
		"list_logs": map[string]any{}, "list_saved_queries": map[string]any{},
		"get_sql_schema": map[string]any{}, "run_sql": map[string]any{"query": "SELECT 1"},
		"run_saved_query": map[string]any{"name": "saved"},
	} {
		t.Run(name, func(t *testing.T) {
			result, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
			require.NoError(t, err)
			require.True(t, result.IsError)
			require.Equal(t, "no authenticated user in context", result.Content[0].(*mcp.TextContent).Text)
		})
	}
}

type mcpRegistryCollections struct {
	core.CollectionStore
	users  []string
	limits []int
}

func (s *mcpRegistryCollections) ListLogsPage(_ context.Context, user, _, _ string, limit int) ([]core.LogSummary, error) {
	s.users = append(s.users, user)
	s.limits = append(s.limits, limit)
	return []core.LogSummary{}, nil
}

func (s *mcpRegistryCollections) ListSavedQueriesPage(_ context.Context, user, _, _ string, limit int) ([]core.SavedQuerySummary, error) {
	s.users = append(s.users, user)
	s.limits = append(s.limits, limit)
	return []core.SavedQuerySummary{}, nil
}

func TestMCPCollectionBindingsUseAuthenticatedActionPipeline(t *testing.T) {
	store := &mcpRegistryCollections{}
	var actions []string
	app := core.New(core.Config{
		Collections: store, OAuth: mcpOAuthStore{}, OAuthIssuer: "https://logs.example.com",
		Middleware: []core.Middleware{func(next core.Handler) core.Handler {
			return func(ctx context.Context, inv core.Invocation) (any, error) {
				actions = append(actions, inv.Action.Name())
				return next(ctx, inv)
			}
		}},
	})
	srv := newMCPServer(app, newOAuthProvider(app, "https://logs.example.com"))
	handler := srv.requireBearerToken()(srv.handler)
	for _, name := range []string{"list_logs", "list_saved_queries"} {
		for _, user := range []string{"alice", "bob"} {
			req := newMCPHTTPRequest(t, "2026-07-28", "tools/call", map[string]any{"name": name, "arguments": map[string]any{}})
			req.Header.Set("Authorization", "Bearer "+user+"-token")
			_, body := serveMCPJSON(t, handler, req)
			require.NotEqual(t, true, body["result"].(map[string]any)["isError"])
		}
	}
	require.Equal(t, []string{"alice", "bob", "alice", "bob"}, store.users)
	require.Equal(t, []int{51, 51, 51, 51}, store.limits, "action validation applies the default page size plus lookahead")
	var collections []string
	for _, name := range actions {
		if name == "list_logs_page" || name == "list_saved_queries_page" {
			collections = append(collections, name)
		}
	}
	require.Equal(t, []string{"list_logs_page", "list_logs_page", "list_saved_queries_page", "list_saved_queries_page"}, collections)
	_, body := serveMCPJSON(t, handler, newMCPHTTPRequest(t, "2026-07-28", "tools/call", map[string]any{"name": "list_logs", "arguments": map[string]any{"limit": 101}}))
	require.Equal(t, true, body["result"].(map[string]any)["isError"])
	require.Len(t, store.users, 4, "invalid action parameters must not reach the store")
}
