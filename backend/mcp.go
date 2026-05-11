package backend

import (
	"context"
	"fmt"
	"net/http"
	"strings"

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
	Logs []logResponse `json:"logs" jsonschema:"the list of logs the authenticated user owns or has been shared on"`
}

func newMCPServer(pool *pgxpool.Pool, oauth *oauthProvider) *mcpServer {
	srv := mcp.NewServer(&mcp.Implementation{
		Name:    "logger4life",
		Title:   "Logger4Life",
		Version: "0.1.0",
	}, nil)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_logs",
		Description: "List all logs the authenticated user owns or has been shared on, ordered alphabetically.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ listLogsInput) (*mcp.CallToolResult, listLogsOutput, error) {
		user := userFromContext(ctx)
		if user == nil {
			return nil, listLogsOutput{}, fmt.Errorf("no authenticated user in context")
		}
		logs, err := listLogsForUser(ctx, pool, user.ID)
		if err != nil {
			return nil, listLogsOutput{}, err
		}
		return nil, listLogsOutput{Logs: logs}, nil
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
