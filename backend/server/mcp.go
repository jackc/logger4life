package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/httplog/v3"
	"github.com/jackc/logger4life/backend/core"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type mcpServer struct {
	server   *mcp.Server
	handler  *mcp.StreamableHTTPHandler
	oauth    *oauthProvider
	requests *keyedRateLimiter
}

type listLogsInput = core.CollectionPageParams
type listLogsOutput = core.LogPage

type getSQLSchemaInput struct{}

type getSQLSchemaOutput struct {
	Views []*core.SQLSchemaView `json:"views" jsonschema:"views the user can query in the sql_query schema, with their columns and comments"`
}

type listSavedQueriesInput = core.CollectionPageParams
type listSavedQueriesOutput = core.SavedQueryPage

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

func readOnlyMCPAnnotations() *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{
		ReadOnlyHint:    true,
		DestructiveHint: new(bool),
		IdempotentHint:  true,
		OpenWorldHint:   new(bool),
	}
}

func newMCPServer(app *core.Core, oauth *oauthProvider) *mcpServer {
	srv := mcp.NewServer(&mcp.Implementation{
		Name:    "logger4life",
		Title:   "Logger4Life",
		Version: "0.1.0",
	}, &mcp.ServerOptions{
		// The tool catalog is fixed at startup; clients do not need a
		// subscriptions/listen stream for tool-list changes.
		Capabilities: &mcp.ServerCapabilities{Tools: &mcp.ToolCapabilities{}},
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_logs",
		Title:       "List logs",
		Annotations: readOnlyMCPAnnotations(),
		Description: "List an alphabetical page of logs the authenticated user owns or has been shared on. Defaults to 50 records, maximum 100; pass next_cursor as cursor to continue. Pages may be shortened to fit the response size limit.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in listLogsInput) (*mcp.CallToolResult, listLogsOutput, error) {
		user, err := requireMCPUser(ctx)
		if err != nil {
			return nil, listLogsOutput{}, err
		}
		page, err := core.ListLogsPage.Call(core.WithUserID(ctx, user.ID), app, in)
		if err != nil {
			return nil, listLogsOutput{}, mcpToolError(ctx, err)
		}
		return nil, page, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "get_sql_schema",
		Title:       "Get SQL schema",
		Annotations: readOnlyMCPAnnotations(),
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
		Title:       "Run SQL query",
		Annotations: readOnlyMCPAnnotations(),
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
		Title:       "List saved queries",
		Annotations: readOnlyMCPAnnotations(),
		Description: "List an alphabetical page of the authenticated user's saved SQL queries, including query text. Defaults to 50 records, maximum 100; pass next_cursor as cursor to continue. Pages may be shortened to fit the response size limit.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in listSavedQueriesInput) (*mcp.CallToolResult, listSavedQueriesOutput, error) {
		user, err := requireMCPUser(ctx)
		if err != nil {
			return nil, listSavedQueriesOutput{}, err
		}
		page, err := core.ListSavedQueriesPage.Call(core.WithUserID(ctx, user.ID), app, in)
		if err != nil {
			return nil, listSavedQueriesOutput{}, mcpToolError(ctx, err)
		}
		return nil, page, nil
	})

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "run_saved_query",
		Title:       "Run saved query",
		Annotations: readOnlyMCPAnnotations(),
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
			// Required for protocol 2026-07-28. The SDK still handles legacy
			// initialize requests, without retaining HTTP session state.
			Stateless: true,
			// These tools return one result and emit no progress notifications.
			JSONResponse: true,
			// Stop database work when a modern client's request disconnects.
			PropagateRequestCancellation: true,
			// We're behind a trusted reverse proxy (Caddy) that may forward
			// the original public Host header while connecting to us over
			// loopback. The SDK's default DNS-rebinding protection treats
			// that combination as a rejection, but the rebinding attack it
			// guards against is handled by the proxy's host routing and the
			// explicit Origin check in requireBearerToken.
			DisableLocalhostProtection: true,
		},
	)
	return &mcpServer{server: srv, handler: handler, oauth: oauth, requests: newKeyedRateLimiter(60, 10)}
}

// requireBearerToken validates an OAuth access token on each request to the
// MCP endpoint. On success it loads the user into the request context using
// the same key as cookie-based loadSession, so userFromContext works inside
// tool handlers.
func (m *mcpServer) requireBearerToken() func(http.Handler) http.Handler {
	resourceMetadataURL := m.oauth.canonicalURL + "/.well-known/oauth-protected-resource"
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Check every method against the configured public origin, even
			// behind a proxy. Non-browser clients may omit Origin entirely.
			if origins := r.Header.Values("Origin"); len(origins) != 0 &&
				(len(origins) != 1 || origins[0] != m.oauth.canonicalURL) {
				http.Error(w, "invalid origin", http.StatusForbidden)
				return
			}
			authz := r.Header.Get("Authorization")
			scheme, token, ok := strings.Cut(authz, " ")
			if !ok || !strings.EqualFold(scheme, "Bearer") {
				writeBearerChallenge(w, resourceMetadataURL, nil)
				return
			}
			token = strings.TrimSpace(token)
			if token == "" {
				writeBearerChallenge(w, resourceMetadataURL, errors.New("missing bearer token"))
				return
			}
			user, err := m.oauth.verifyAccessToken(r.Context(), token)
			if err != nil {
				writeBearerChallenge(w, resourceMetadataURL, err)
				return
			}
			ctx := context.WithValue(r.Context(), userContextKey, user)
			// Count every authenticated POST, including legacy calls whose
			// method is only in the body. No header or token rotation can
			// bypass the user's allowance; discovery GETs remain available.
			if r.Method == http.MethodPost {
				if retry := m.requests.allow(user.ID); retry > 0 {
					writeRateLimit(w, retry)
					return
				}
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// writeBearerChallenge points clients at our protected-resource metadata.
// Invalid credentials receive 401; valid tokens lacking MCP permission receive
// 403 with insufficient_scope and the required scope (RFC 6750 §3.1).
func writeBearerChallenge(w http.ResponseWriter, resourceMetadataURL string, err error) {
	challenge := fmt.Sprintf(`Bearer resource_metadata=%q, scope=%q`, resourceMetadataURL, core.OAuthScopeMCP)
	status := http.StatusUnauthorized
	body := map[string]string{"error": "unauthorized"}
	// RFC 6750 §3.1: an initial challenge without credentials should not
	// claim that the client presented an invalid token.
	if err != nil {
		code := "invalid_token"
		if errors.Is(err, core.ErrOAuthInsufficientScope) {
			status = http.StatusForbidden
			code = "insufficient_scope"
			body["error"] = code
		}
		challenge += fmt.Sprintf(`, error=%q, error_description=%q`, code, err.Error())
		body["error_description"] = err.Error()
	}
	w.Header().Set("WWW-Authenticate", challenge)
	writeJSON(w, status, body)
}
