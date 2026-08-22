package backend

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/logger4life/backend/core"
	"github.com/modelcontextprotocol/go-sdk/mcp"
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
func (mcpSavedQueryStore) CreateSavedQuery(context.Context, string, string, string) (core.SavedQuery, error) {
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
