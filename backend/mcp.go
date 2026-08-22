package backend

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/go-chi/httplog/v3"
	"github.com/jackc/logger4life/backend/core"
	"github.com/jackc/logger4life/backend/pgstore"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type mcpServer struct {
	server  *mcp.Server
	handler *mcp.StreamableHTTPHandler
	oauth   *oauthProvider
}

// listLogsInput is the (empty) input schema for the list_logs tool.
// Even though we take no parameters, the MCP SDK requires a struct/map type
// so an "object" JSON schema can be inferred.
type listLogsInput struct{}

// listLogsOutput mirrors the HTTP /api/logs response shape.
type listLogsOutput struct {
	Logs []core.Log `json:"logs" jsonschema:"the list of logs the authenticated user owns or has been shared on"`
}

type getSQLSchemaInput struct{}

type getSQLSchemaOutput struct {
	Views []*core.SQLSchemaView `json:"views" jsonschema:"views the user can query in the sql_query schema, with their columns and comments"`
}

type listSavedQueriesInput struct{}

type listSavedQueriesOutput struct {
	Queries []core.SavedQuery `json:"queries" jsonschema:"the user's saved SQL queries, ordered alphabetically by name"`
}

type runSavedQueryInput struct {
	Name string `json:"name" jsonschema:"name of the saved query to run (case-sensitive, as returned by list_saved_queries)"`
}

type runSQLInput struct {
	Query string `json:"query" jsonschema:"a read-only SELECT against the sql_query schema views (logs, log_entries)"`
}

// runSQLOutput is the same core result returned by /api/sql/execute.
type runSQLOutput = core.UserSQLResult

// mcpToolError exposes only core validation and explicitly safe query
// failures. Every other error is logged and replaced so database, pool, and
// IO details cannot reach the MCP client.
func mcpToolError(ctx context.Context, err error) error {
	var validationErr *core.ValidationError
	if errors.As(err, &validationErr) {
		return errors.New(validationErr.Err.Error())
	}
	var queryFailure *core.UserSQLFailure
	if errors.As(err, &queryFailure) {
		return errors.New(queryFailure.Error())
	}
	httplog.SetError(ctx, err)
	return errors.New("internal error")
}

// requireMCPUser pulls the AuthUser attached to the request context by
// requireBearerToken. The middleware already rejects unauthenticated
// requests, so a nil result indicates a wiring bug rather than user input;
// we surface it as an explicit error to keep tool handlers from
// dereferencing a nil pointer.
func requireMCPUser(ctx context.Context) (*AuthUser, error) {
	user := userFromContext(ctx)
	if user == nil {
		return nil, errors.New("no authenticated user in context")
	}
	return user, nil
}

func newMCPServer(pool *pgxpool.Pool, oauth *oauthProvider, configured ...*core.Core) *mcpServer {
	store := pgstore.New(pool)
	app := core.New(core.Config{Logs: store, Entries: store, Placements: store, SavedQueries: store, SQLSchema: store, UserSQL: store})
	if len(configured) > 0 {
		app = configured[0]
	}
	srv := mcp.NewServer(&mcp.Implementation{
		Name:    "logger4life",
		Title:   "Logger4Life",
		Version: "0.1.0",
	}, nil)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_logs",
		Description: "List all logs the authenticated user owns or has been shared on, ordered alphabetically.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ listLogsInput) (*mcp.CallToolResult, listLogsOutput, error) {
		user, err := requireMCPUser(ctx)
		if err != nil {
			return nil, listLogsOutput{}, err
		}
		logs, err := core.ListLogs.Call(core.WithUserID(ctx, user.ID), app, core.ListLogsParams{})
		if err != nil {
			return nil, listLogsOutput{}, mcpToolError(ctx, err)
		}
		// The action returns rows in user-organized (folder, position)
		// order for the SPA. The MCP contract promises alphabetical, so sort
		// here rather than coupling the tool to UI organization.
		sort.SliceStable(logs, func(i, j int) bool {
			return strings.ToLower(logs[i].Name) < strings.ToLower(logs[j].Name)
		})
		return nil, listLogsOutput{Logs: logs}, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "get_sql_schema",
		Description: "Describe the read-only views available for SQL queries (sql_query.logs and sql_query.log_entries) including columns, types, and per-column comments. Call this before writing a query to know what to select.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ getSQLSchemaInput) (*mcp.CallToolResult, getSQLSchemaOutput, error) {
		if _, err := requireMCPUser(ctx); err != nil {
			return nil, getSQLSchemaOutput{}, err
		}
		result, err := core.GetSQLSchema.Call(ctx, app, core.GetSQLSchemaParams{})
		if err != nil {
			return nil, getSQLSchemaOutput{}, mcpToolError(ctx, err)
		}
		return nil, getSQLSchemaOutput{Views: result.Views}, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "run_sql",
		Description: "Run a read-only SELECT against the sql_query schema views as the authenticated user. Only SELECT statements on the logs and log_entries views are allowed; results are capped at 1000 rows.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in runSQLInput) (*mcp.CallToolResult, runSQLOutput, error) {
		user, err := requireMCPUser(ctx)
		if err != nil {
			return nil, runSQLOutput{}, err
		}
		result, err := core.ExecuteUserSQL.Call(core.WithUserID(ctx, user.ID), app, core.ExecuteUserSQLParams{Query: in.Query})
		if err != nil {
			return nil, runSQLOutput{}, mcpToolError(ctx, err)
		}
		return nil, result, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_saved_queries",
		Description: "List the authenticated user's saved SQL queries, ordered alphabetically by name. Each entry includes the query text so a follow-up run_sql call can execute or adapt it.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ listSavedQueriesInput) (*mcp.CallToolResult, listSavedQueriesOutput, error) {
		user, err := requireMCPUser(ctx)
		if err != nil {
			return nil, listSavedQueriesOutput{}, err
		}
		queries, err := core.ListSavedQueries.Call(core.WithUserID(ctx, user.ID), app, core.ListSavedQueriesParams{})
		if err != nil {
			return nil, listSavedQueriesOutput{}, mcpToolError(ctx, err)
		}
		return nil, listSavedQueriesOutput{Queries: queries}, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "run_saved_query",
		Description: "Look up a saved query by name and execute it. Equivalent to calling list_saved_queries then run_sql with the matching query_text.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in runSavedQueryInput) (*mcp.CallToolResult, runSQLOutput, error) {
		user, err := requireMCPUser(ctx)
		if err != nil {
			return nil, runSQLOutput{}, err
		}
		name := strings.TrimSpace(in.Name)
		if name == "" {
			return nil, runSQLOutput{}, fmt.Errorf("name is required")
		}
		saved, err := core.GetSavedQuery.Call(core.WithUserID(ctx, user.ID), app, core.GetSavedQueryParams{Name: name})
		if err != nil {
			if errors.Is(err, core.ErrSavedQueryNotFound) {
				return nil, runSQLOutput{}, fmt.Errorf("no saved query named %q", name)
			}
			return nil, runSQLOutput{}, mcpToolError(ctx, err)
		}
		result, err := core.ExecuteUserSQL.Call(core.WithUserID(ctx, user.ID), app, core.ExecuteUserSQLParams{Query: saved.QueryText})
		if err != nil {
			return nil, runSQLOutput{}, mcpToolError(ctx, err)
		}
		return nil, result, nil
	})

	handler := mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return srv },
		&mcp.StreamableHTTPOptions{
			// We're behind a trusted reverse proxy (Caddy) that may forward
			// the original public Host header while connecting to us over
			// loopback. The SDK's default DNS-rebinding protection treats
			// that combination as a rejection, but the rebinding attack it
			// guards against doesn't apply when the upstream is reached via
			// a verified reverse proxy.
			DisableLocalhostProtection: true,
		},
	)
	return &mcpServer{server: srv, handler: handler, oauth: oauth}
}

// requireBearerToken validates an OAuth access token on each request to the
// MCP endpoint. On success it loads the user into the request context using
// the same key as cookie-based loadSession, so userFromContext works inside
// tool handlers.
func (m *mcpServer) requireBearerToken() func(http.Handler) http.Handler {
	resourceMetadataURL := m.oauth.canonicalURL + "/.well-known/oauth-protected-resource"
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authz := r.Header.Get("Authorization")
			if !strings.HasPrefix(authz, "Bearer ") {
				writeBearerChallenge(w, resourceMetadataURL, "missing bearer token")
				return
			}
			token := strings.TrimSpace(strings.TrimPrefix(authz, "Bearer "))
			if token == "" {
				writeBearerChallenge(w, resourceMetadataURL, "missing bearer token")
				return
			}
			user, err := m.oauth.verifyAccessToken(r.Context(), token)
			if err != nil {
				writeBearerChallenge(w, resourceMetadataURL, err.Error())
				return
			}
			ctx := context.WithValue(r.Context(), userContextKey, user)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// writeBearerChallenge emits a 401 with the WWW-Authenticate header pointing
// the client at our protected-resource metadata, per RFC 9728 §5.1.
func writeBearerChallenge(w http.ResponseWriter, resourceMetadataURL, errDesc string) {
	w.Header().Set("WWW-Authenticate",
		fmt.Sprintf(`Bearer resource_metadata=%q, error="invalid_token", error_description=%q`,
			resourceMetadataURL, errDesc))
	writeJSON(w, http.StatusUnauthorized, map[string]string{
		"error":             "unauthorized",
		"error_description": errDesc,
	})
}
