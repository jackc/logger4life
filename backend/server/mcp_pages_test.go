package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/jackc/logger4life/backend/core"
	"github.com/stretchr/testify/require"
)

type mcpCollectionStore struct{ core.CollectionStore }

func (mcpCollectionStore) ListSavedQueriesPage(context.Context, string, string, string, int) ([]core.SavedQuerySummary, error) {
	rows := make([]core.SavedQuerySummary, 100)
	for i := range rows {
		rows[i] = core.SavedQuerySummary{ID: fmt.Sprintf("%03d", i), Name: "query", SortName: "query", QueryText: strings.Repeat(`"<&`, 3000)}
	}
	return rows, nil
}

func TestMCPCollectionResponseLimits(t *testing.T) {
	app := core.New(core.Config{Collections: mcpCollectionStore{}, OAuth: mcpOAuthStore{}, OAuthIssuer: "https://logs.example.com"})
	srv := newMCPServer(app, newOAuthProvider(app, "https://logs.example.com"))
	handler := srv.requireBearerToken()(srv.handler)
	w, body := serveMCPJSON(t, handler, newMCPHTTPRequest(t, "2026-07-28", "tools/call", map[string]any{"name": "list_saved_queries", "arguments": map[string]any{"limit": 100}}))
	require.Equal(t, http.StatusOK, w.Code)
	require.Less(t, w.Body.Len(), 1<<20, "cap must include SDK text/structured duplication and JSON escaping")
	result := body["result"].(map[string]any)
	require.NotEqual(t, true, result["isError"], w.Body.String())
	structured := result["structuredContent"].(map[string]any)
	require.NotEmpty(t, structured["next_cursor"])
	require.Less(t, len(structured["queries"].([]any)), 100)
	_, body = serveMCPJSON(t, handler, newMCPHTTPRequest(t, "2026-07-28", "tools/list", nil))
	for _, item := range body["result"].(map[string]any)["tools"].([]any) {
		tool := item.(map[string]any)
		if tool["name"] != "list_logs" && tool["name"] != "list_saved_queries" {
			continue
		}
		input, err := json.Marshal(tool["inputSchema"])
		require.NoError(t, err)
		require.Contains(t, string(input), "cursor")
		require.Contains(t, string(input), "limit")
		output, err := json.Marshal(tool["outputSchema"])
		require.NoError(t, err)
		for _, hidden := range []string{"SortName", "share_token", "folder_id", "home_position"} {
			require.NotContains(t, string(output), hidden)
		}
	}
}
