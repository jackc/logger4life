package server

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/httplog/v3"
	"github.com/jackc/logger4life/backend/core"
	"github.com/jackc/logger4life/httpregistry"
)

// HTTPMetadata is Logger4Life's HTTP policy, independent of core.Public.
// Public permits anonymous access; Credentials selects the endpoint gate.
// The global optional session loader still runs on every request, including MCP.
type HTTPMetadata struct {
	Public      bool   `json:"public"`
	Credentials string `json:"credentials"`
}

// HTTPInventory assembles the same declarations as Run without opening stores,
// invoking actions, starting jobs, or listening. Handler constructors only close
// over dependencies; this function never exposes those non-serving handlers.
func HTTPInventory(cfg Config) (httpregistry.Inventory[HTTPMetadata], error) {
	if err := cfg.normalizeMCPCanonicalURL(); err != nil {
		return httpregistry.Inventory[HTTPMetadata]{}, err
	}
	if err := cfg.validateLimits(); err != nil {
		return httpregistry.Inventory[HTTPMetadata]{}, err
	}
	return newHTTPRegistry(nil, nil, cfg, slog.New(slog.DiscardHandler), nil).Inventory(), nil
}

func newHTTPRegistry(app *core.Core, health HealthCheck, cfg Config, logger *slog.Logger, oauth *oauthProvider) *httpregistry.Registry[HTTPMetadata] {
	// Shared protocol services are only constructed if an enabled endpoint
	// requests them. Disabled declarations still appear in the inventory.
	protocol := sync.OnceValue(func() *mcpServer {
		if oauth == nil {
			oauth = newOAuthProvider(app, cfg.MCPCanonicalURL)
		}
		// Configuration applies equally to production and test-supplied providers.
		oauth.registrationIPs = newKeyedRateLimiter(limitOrDefault(cfg.OAuthRegistrationPerIP, 5), 5)
		oauth.registrations = newKeyedRateLimiter(limitOrDefault(cfg.OAuthRegistrationGlobal, 30), 10)
		oauth.trustedProxies, _ = parseTrustedProxies(cfg.TrustedProxyCIDRs)
		mcp := newMCPServer(app, oauth)
		mcp.requests = newKeyedRateLimiter(limitOrDefault(cfg.MCPRequestsPerMinute, 60), limitOrDefault(cfg.MCPRequestBurst, 10))
		return mcp
	})
	compiler := func(spec httpregistry.Spec[HTTPMetadata]) ([]httpregistry.Stage, error) {
		switch spec.Metadata.Credentials {
		case "session":
			if spec.Metadata.Public {
				return nil, nil
			}
			return []httpregistry.Stage{{Name: "require-session-user", Wrap: requireAuth}}, nil
		case "oauth-bearer":
			if spec.Metadata.Public {
				return nil, fmt.Errorf("bearer endpoints cannot be public")
			}
			return []httpregistry.Stage{{Name: "mcp-origin-bearer-and-post-rate-limit", Wrap: func(next http.Handler) http.Handler {
				return protocol().requireBearerToken()(next)
			}}}, nil
		default:
			return nil, fmt.Errorf("unknown credential policy %q", spec.Metadata.Credentials)
		}
	}
	r := httpregistry.New(compiler,
		httpregistry.Stage{Name: "request-id", Wrap: middleware.RequestID},
		httpregistry.Stage{Name: "response-request-id", Wrap: func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if id := middleware.GetReqID(r.Context()); id != "" {
					w.Header().Set(middleware.RequestIDHeader, id)
				}
				next.ServeHTTP(w, r)
			})
		}},
		httpregistry.Stage{Name: "optional-session-loader", Wrap: loadSession(app)},
		httpregistry.Stage{Name: "request-log-and-recovery", Wrap: httplog.RequestLogger(logger, &httplog.Options{
			Level: cfg.SlogLevel(), RecoverPanics: true,
			LogExtraAttrs: func(req *http.Request, _ string, _ int) []slog.Attr {
				var attrs []slog.Attr
				if id := middleware.GetReqID(req.Context()); id != "" {
					attrs = append(attrs, slog.String("request_id", id))
				}
				if user := userFromContext(req.Context()); user != nil {
					attrs = append(attrs, slog.String("user_id", user.ID), slog.String("username", user.Username))
				}
				return attrs
			},
		})},
	)
	public := HTTPMetadata{Public: true, Credentials: "session"}
	session := HTTPMetadata{Credentials: "session"}
	bearer := HTTPMetadata{Credentials: "oauth-bearer"}
	passkeys := &httpregistry.Condition{Name: "passkeys-enabled", Enabled: cfg.PasskeysEnabled()}
	mcp := &httpregistry.Condition{Name: "mcp-enabled", Enabled: cfg.MCPEnabled()}
	// The helper only shortens syntax: every call supplies its complete policy.
	handle := func(method, path string, policy HTTPMetadata, condition *httpregistry.Condition, h http.Handler) {
		r.Handle(httpregistry.Spec[HTTPMetadata]{Method: method, Path: path, Metadata: policy, When: condition}, httpregistry.Custom(h))
	}
	handle("GET", "/health", public, nil, handleHealth(health))
	handle("GET", "/api/hello", public, nil, handleHello(health))
	handle("GET", "/api/settings", public, nil, handleSettings(cfg))
	// Registration remains installed even when the handler's feature is disabled.
	handle("POST", "/api/register", public, nil, handleRegister(app, cfg.AllowRegistration))
	handle("POST", "/api/login", public, nil, handleLogin(app))
	handle("POST", "/api/passkey-login/begin", public, passkeys, handlePasskeyLoginBegin(app))
	handle("POST", "/api/passkey-login/finish", public, passkeys, handlePasskeyLoginFinish(app))
	// Method expressions select handlers without constructing the provider.
	oauthRoute := func(method, path string, factory func(*oauthProvider) http.HandlerFunc) {
		r.Handle(httpregistry.Spec[HTTPMetadata]{Method: method, Path: path, Metadata: public, When: mcp},
			httpregistry.CustomFactory(func() http.Handler { return factory(protocol().oauth) }))
	}
	oauthRoute("GET", "/.well-known/oauth-protected-resource", (*oauthProvider).handleProtectedResourceMetadata)
	oauthRoute("GET", "/.well-known/oauth-authorization-server", (*oauthProvider).handleAuthorizationServerMetadata)
	oauthRoute("POST", "/oauth/register", (*oauthProvider).handleDynamicClientRegistration)
	oauthRoute("GET", "/oauth/authorize", (*oauthProvider).handleAuthorize)
	oauthRoute("POST", "/oauth/authorize", (*oauthProvider).handleAuthorize)
	oauthRoute("POST", "/oauth/token", (*oauthProvider).handleToken)
	oauthRoute("POST", "/oauth/revoke", (*oauthProvider).handleRevoke)
	r.Mount(httpregistry.Spec[HTTPMetadata]{Path: "/mcp", Metadata: bearer, When: mcp},
		httpregistry.CustomFactory(func() http.Handler { return protocol().handler }))
	handle("POST", "/api/logout", session, nil, handleLogout(app))
	handle("GET", "/api/me", session, nil, handleMe(app))
	handle("PUT", "/api/me/email", session, nil, handleChangeEmail(app))
	handle("PUT", "/api/me/password", session, nil, handleChangePassword(app))
	handle("GET", "/api/me/passkeys", session, passkeys, handleListPasskeys(app))
	handle("PUT", "/api/me/passkeys/{passkeyID}", session, passkeys, handleUpdatePasskey(app))
	handle("DELETE", "/api/me/passkeys/{passkeyID}", session, passkeys, handleDeletePasskey(app))
	handle("POST", "/api/me/passkeys/register/begin", session, passkeys, handlePasskeyRegisterBegin(app))
	handle("POST", "/api/me/passkeys/register/finish", session, passkeys, handlePasskeyRegisterFinish(app))
	handle("POST", "/api/logs", session, nil, handleCreateLog(app))
	r.Handle(httpregistry.Spec[HTTPMetadata]{Method: "GET", Path: "/api/logs", Metadata: session}, bindHTTPAction(core.ListLogs, app, httpregistry.Binding{
		JSON: httpregistry.NoJSON, Status: http.StatusOK,
		Errors: httpregistry.ErrorWriter{Name: "internal-error", Write: internalError},
	}))
	r.Handle(httpregistry.Spec[HTTPMetadata]{Method: "GET", Path: "/api/logs/{logID}", Metadata: session}, bindHTTPAction(core.GetLog, app, httpregistry.Binding{
		JSON: httpregistry.NoJSON, Status: http.StatusOK,
		Inputs: []httpregistry.Input{{Source: "path", Name: "logID", Field: "LogID"}},
		Errors: httpregistry.ErrorWriter{Name: "log-operation-error", Write: writeLogOperationError},
	}))
	handle("PUT", "/api/logs/{logID}", session, nil, handleUpdateLog(app))
	handle("DELETE", "/api/logs/{logID}", session, nil, handleDeleteLog(app))
	handle("PUT", "/api/logs/{logID}/placement", session, nil, handleUpdateLogPlacement(app))
	handle("PUT", "/api/logs/{logID}/pin", session, nil, handlePinLog(app))
	handle("PUT", "/api/logs/{logID}/home-position", session, nil, handleUpdateLogHomePosition(app))
	handle("POST", "/api/folders", session, nil, handleCreateFolder(app))
	handle("GET", "/api/folders", session, nil, handleListFolders(app))
	handle("PUT", "/api/folders/{folderID}", session, nil, handleRenameFolder(app))
	handle("PUT", "/api/folders/{folderID}/move", session, nil, handleMoveFolder(app))
	handle("DELETE", "/api/folders/{folderID}", session, nil, handleDeleteFolder(app))
	r.Handle(httpregistry.Spec[HTTPMetadata]{Method: "POST", Path: "/api/logs/{logID}/entries", Metadata: session}, bindHTTPAction(core.CreateLogEntry, app, httpregistry.Binding{
		JSON: httpregistry.StrictJSON, Status: http.StatusCreated,
		Inputs: []httpregistry.Input{{Source: "path", Name: "logID", Field: "LogID"}, {Source: "body", Name: "note", Field: "Note"}, {Source: "body", Name: "fields", Field: "Fields"}},
		Errors: httpregistry.ErrorWriter{Name: "log-operation-error", Write: writeLogOperationError},
	}))
	handle("GET", "/api/logs/{logID}/entries", session, nil, handleListLogEntries(app))
	handle("PUT", "/api/logs/{logID}/entries/{entryID}", session, nil, handleUpdateLogEntry(app))
	handle("DELETE", "/api/logs/{logID}/entries/{entryID}", session, nil, handleDeleteLogEntry(app))
	handle("POST", "/api/logs/{logID}/share-token", session, nil, handleCreateShareToken(app))
	handle("DELETE", "/api/logs/{logID}/share-token", session, nil, handleDeleteShareToken(app))
	handle("GET", "/api/logs/{logID}/shares", session, nil, handleListShares(app))
	handle("DELETE", "/api/logs/{logID}/shares/{shareID}", session, nil, handleRemoveShare(app))
	handle("GET", "/api/join/{token}", session, nil, handleGetShareInfo(app))
	handle("POST", "/api/join/{token}", session, nil, handleJoinLog(app))
	handle("POST", "/api/sql/execute", session, nil, handleExecuteSQL(app))
	handle("GET", "/api/sql/schema", session, nil, handleGetSQLSchema(app))
	handle("GET", "/api/sql/saved", session, nil, handleListSavedQueries(app))
	handle("POST", "/api/sql/saved", session, nil, handleCreateSavedQuery(app))
	handle("PUT", "/api/sql/saved/{id}", session, nil, handleUpdateSavedQuery(app))
	handle("DELETE", "/api/sql/saved/{id}", session, nil, handleDeleteSavedQuery(app))
	return r
}

// bindHTTPAction supplies application-wide binding conventions once. Transport
// decoding failures are handled before the endpoint's domain error policy.
func bindHTTPAction[P, R any](operation *core.Action[P, R], app *core.Core, spec httpregistry.Binding) httpregistry.Endpoint {
	if spec.Errors.Name == "" || spec.Errors.Write == nil {
		panic("server: binding domain error writer is required")
	}
	spec.Namespace = "github.com/jackc/logger4life/backend/core"
	domainErrors := spec.Errors
	inputMessage := "invalid request parameters"
	if spec.JSON != httpregistry.NoJSON {
		inputMessage = "invalid request body" // Preserve the existing JSON error response.
	}
	spec.Errors = httpregistry.ErrorWriter{
		Name: "binding-input-and-" + domainErrors.Name,
		Write: func(w http.ResponseWriter, r *http.Request, err error) {
			var input *httpregistry.InputError
			if errors.As(err, &input) {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": inputMessage})
				return
			}
			domainErrors.Write(w, r, err)
		},
	}
	return httpregistry.Bind(operation, app, spec)
}
