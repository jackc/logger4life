package server

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/httplog/v3"
	"github.com/jackc/logger4life/backend/core"
	"github.com/jackc/manifest/mcphttp"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type mcpServer struct {
	server   *mcp.Server
	handler  *mcp.StreamableHTTPHandler
	oauth    *oauthProvider
	requests *keyedRateLimiter
}

type getSQLSchemaInput struct{}

type getSQLSchemaOutput struct {
	Views []*core.SQLSchemaView `json:"views" jsonschema:"views the user can query in the sql_query schema, with their columns and comments"`
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

func newMCPServer(app *core.Core, oauth *oauthProvider) *mcpServer {
	srv := newMCPRegistry(app).Server()

	// mcphttp's options rely on requireBearerToken's Origin check and on
	// the trusted reverse proxy (Caddy) routing by host.
	handler := mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return srv },
		mcphttp.StreamableHTTPOptions(),
	)
	return &mcpServer{server: srv, handler: handler, oauth: oauth, requests: newKeyedRateLimiter(60, 10)}
}

// requireBearerToken validates an OAuth access token on each request to the
// MCP endpoint. On success it loads the user into the request context using
// the same key as cookie-based loadSession, so userFromContext works inside
// tool handlers.
func (m *mcpServer) requireBearerToken() func(http.Handler) http.Handler {
	return mcphttp.RequireBearer(mcphttp.Bearer{
		Origin:              m.oauth.canonicalURL,
		ResourceMetadataURL: m.oauth.canonicalURL + "/.well-known/oauth-protected-resource",
		Scope:               core.OAuthScopeMCP,
		Verify: func(ctx context.Context, token string) (context.Context, error) {
			user, err := m.oauth.verifyAccessToken(ctx, token)
			switch {
			case errors.Is(err, core.ErrOAuthInsufficientScope):
				return nil, mcphttp.InsufficientScope(err)
			case err != nil:
				return nil, err
			}
			return context.WithValue(ctx, userContextKey, user), nil
		},
		LimitPost: func(ctx context.Context) time.Duration {
			return m.requests.allow(userFromContext(ctx).ID)
		},
	})
}
