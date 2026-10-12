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

func readOnlyMCPAnnotations() *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{
		ReadOnlyHint:    true,
		DestructiveHint: new(bool),
		IdempotentHint:  true,
		OpenWorldHint:   new(bool),
	}
}

func newMCPServer(app *core.Core, oauth *oauthProvider) *mcpServer {
	srv := newMCPRegistry(app).Server()

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
