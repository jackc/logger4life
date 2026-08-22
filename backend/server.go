package backend

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/httplog/v3"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/jackc/logger4life/backend/core"
	"github.com/jackc/logger4life/backend/pgstore"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"
)

var serverCmd = &cobra.Command{
	Use:   "server",
	Short: "Start the HTTP server",
	RunE:  runServer,
}

var (
	flagDatabaseURL       string
	flagBindAddress       string
	flagPort              string
	flagAllowRegistration bool
	flagWebAuthnRPID      string
	flagWebAuthnOrigin    string
	flagLogLevel          string
	flagLogFormat         string
	flagMCPCanonicalURL   string
	flagSecureCookies     bool
)

func init() {
	rootCmd.AddCommand(serverCmd)
	serverCmd.Flags().StringVar(&flagDatabaseURL, "database-url", "", "database connection URL")
	serverCmd.Flags().StringVar(&flagBindAddress, "bind-address", "", "address to bind to (default 127.0.0.1)")
	serverCmd.Flags().StringVar(&flagPort, "port", "", "port to listen on (default 4000)")
	serverCmd.Flags().BoolVar(&flagAllowRegistration, "allow-registration", false, "allow new user registration")
	serverCmd.Flags().StringVar(&flagWebAuthnRPID, "webauthn-rp-id", "", "WebAuthn relying party ID")
	serverCmd.Flags().StringVar(&flagWebAuthnOrigin, "webauthn-origin", "", "WebAuthn origin URL")
	serverCmd.Flags().StringVar(&flagLogLevel, "log-level", "", "log level (debug, info, warn, error)")
	serverCmd.Flags().StringVar(&flagLogFormat, "log-format", "", "log format (json, text, journal)")
	serverCmd.Flags().StringVar(&flagMCPCanonicalURL, "mcp-canonical-url", "", "public canonical URL of this server; enables MCP+OAuth routes when set")
	serverCmd.Flags().BoolVar(&flagSecureCookies, "secure-cookies", false, "set the Secure attribute on session cookies (required behind HTTPS)")
}

func runServer(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	cfg := ConfigFromEnv()

	if cmd.Flags().Changed("database-url") {
		cfg.DatabaseURL = flagDatabaseURL
	}
	if cmd.Flags().Changed("bind-address") {
		cfg.BindAddress = flagBindAddress
	}
	if cmd.Flags().Changed("port") {
		cfg.Port = flagPort
	}
	if cmd.Flags().Changed("allow-registration") {
		cfg.AllowRegistration = flagAllowRegistration
	}
	if cmd.Flags().Changed("webauthn-rp-id") {
		cfg.WebAuthnRPID = flagWebAuthnRPID
	}
	if cmd.Flags().Changed("webauthn-origin") {
		cfg.WebAuthnOrigin = flagWebAuthnOrigin
	}
	if cmd.Flags().Changed("log-level") {
		cfg.LogLevel = flagLogLevel
	}
	if cmd.Flags().Changed("log-format") {
		cfg.LogFormat = flagLogFormat
	}
	if cmd.Flags().Changed("mcp-canonical-url") {
		cfg.MCPCanonicalURL = strings.TrimRight(flagMCPCanonicalURL, "/")
	}
	if cmd.Flags().Changed("secure-cookies") {
		cfg.SecureCookies = flagSecureCookies
	}

	secureCookies = cfg.SecureCookies

	handler, err := cfg.SlogHandler()
	if err != nil {
		return fmt.Errorf("unable to initialize logger: %w", err)
	}
	logger := slog.New(handler)

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("unable to connect to database: %w", err)
	}
	defer pool.Close()

	err = pool.QueryRow(ctx, "select 1").Scan(new(int))
	if err != nil {
		return fmt.Errorf("unable to query database: %w", err)
	}
	logger.Info("Database connected")
	store := pgstore.New(pool)
	var wan *webauthn.WebAuthn
	if cfg.PasskeysEnabled() {
		wan, err = webauthn.New(&webauthn.Config{
			RPDisplayName: "Logger4Life",
			RPID:          cfg.WebAuthnRPID,
			RPOrigins:     []string{cfg.WebAuthnOrigin},
		})
		if err != nil {
			return fmt.Errorf("unable to initialize webauthn: %w", err)
		}
	}
	app := core.New(core.Config{Users: store, Sessions: store, Passkeys: store, Challenges: store, WebAuthn: wan, Tx: store, Logs: store, Entries: store, Placements: store, Folders: store, SavedQueries: store, SQLSchema: store, UserSQL: store, Sharing: store, OAuth: store, OAuthIssuer: cfg.MCPCanonicalURL})

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if reqID := middleware.GetReqID(r.Context()); reqID != "" {
				w.Header().Set(middleware.RequestIDHeader, reqID)
			}
			next.ServeHTTP(w, r)
		})
	})
	r.Use(loadSession(app))
	r.Use(httplog.RequestLogger(logger, &httplog.Options{
		Level:         cfg.SlogLevel(),
		RecoverPanics: true,
		LogExtraAttrs: func(req *http.Request, _ string, _ int) []slog.Attr {
			var attrs []slog.Attr
			if reqID := middleware.GetReqID(req.Context()); reqID != "" {
				attrs = append(attrs, slog.String("request_id", reqID))
			}
			if user := userFromContext(req.Context()); user != nil {
				attrs = append(attrs, slog.String("user_id", user.ID))
				attrs = append(attrs, slog.String("username", user.Username))
			}
			return attrs
		},
	}))

	// Health check
	r.Get("/health", handleHealth(pool))

	// Public routes
	r.Get("/api/hello", handleHello(pool))
	r.Get("/api/settings", handleSettings(cfg))
	r.Post("/api/register", handleRegister(app, cfg.AllowRegistration))
	r.Post("/api/login", handleLogin(app))
	if wan != nil {
		r.Post("/api/passkey-login/begin", handlePasskeyLoginBegin(app))
		r.Post("/api/passkey-login/finish", handlePasskeyLoginFinish(app))
	}

	// OAuth + MCP. Mounted only when a canonical URL is configured (because
	// the canonical URL is needed both as the OAuth issuer and as the RFC
	// 8707 audience binding for issued access tokens).
	if cfg.MCPEnabled() {
		oauth := newOAuthProvider(app, cfg.MCPCanonicalURL)
		mcpSrv := newMCPServer(app, oauth)

		r.Get("/.well-known/oauth-protected-resource", oauth.handleProtectedResourceMetadata())
		r.Get("/.well-known/oauth-authorization-server", oauth.handleAuthorizationServerMetadata())
		r.Post("/oauth/register", oauth.handleDynamicClientRegistration())
		r.Get("/oauth/authorize", oauth.handleAuthorize())
		r.Post("/oauth/authorize", oauth.handleAuthorize())
		r.Post("/oauth/token", oauth.handleToken())
		r.Post("/oauth/revoke", oauth.handleRevoke())

		// /mcp authenticates via bearer token, NOT via the cookie-session
		// loadSession+requireAuth path used for the SPA's /api routes.
		r.Group(func(r chi.Router) {
			r.Use(mcpSrv.requireBearerToken())
			r.Mount("/mcp", mcpSrv.handler)
		})

		logger.Info("MCP enabled", "canonical_url", cfg.MCPCanonicalURL)
	}

	// Protected routes
	r.Group(func(r chi.Router) {
		r.Use(requireAuth)
		r.Post("/api/logout", handleLogout(app))
		r.Get("/api/me", handleMe(app))
		r.Put("/api/me/email", handleChangeEmail(app))
		r.Put("/api/me/password", handleChangePassword(app))
		if wan != nil {
			r.Get("/api/me/passkeys", handleListPasskeys(app))
			r.Put("/api/me/passkeys/{passkeyID}", handleUpdatePasskey(app))
			r.Delete("/api/me/passkeys/{passkeyID}", handleDeletePasskey(app))
			r.Post("/api/me/passkeys/register/begin", handlePasskeyRegisterBegin(app))
			r.Post("/api/me/passkeys/register/finish", handlePasskeyRegisterFinish(app))
		}

		// Logs
		r.Post("/api/logs", handleCreateLog(app))
		r.Get("/api/logs", handleListLogs(app))
		r.Get("/api/logs/{logID}", handleGetLog(app))
		r.Put("/api/logs/{logID}", handleUpdateLog(app))
		r.Delete("/api/logs/{logID}", handleDeleteLog(app))
		r.Put("/api/logs/{logID}/placement", handleUpdateLogPlacement(app))
		r.Put("/api/logs/{logID}/pin", handlePinLog(app))
		r.Put("/api/logs/{logID}/home-position", handleUpdateLogHomePosition(app))

		// Folders
		r.Post("/api/folders", handleCreateFolder(app))
		r.Get("/api/folders", handleListFolders(app))
		r.Put("/api/folders/{folderID}", handleRenameFolder(app))
		r.Put("/api/folders/{folderID}/move", handleMoveFolder(app))
		r.Delete("/api/folders/{folderID}", handleDeleteFolder(app))

		// Log entries
		r.Post("/api/logs/{logID}/entries", handleCreateLogEntry(app))
		r.Get("/api/logs/{logID}/entries", handleListLogEntries(app))
		r.Put("/api/logs/{logID}/entries/{entryID}", handleUpdateLogEntry(app))
		r.Delete("/api/logs/{logID}/entries/{entryID}", handleDeleteLogEntry(app))

		// Sharing
		r.Post("/api/logs/{logID}/share-token", handleCreateShareToken(app))
		r.Delete("/api/logs/{logID}/share-token", handleDeleteShareToken(app))
		r.Get("/api/logs/{logID}/shares", handleListShares(app))
		r.Delete("/api/logs/{logID}/shares/{shareID}", handleRemoveShare(app))
		r.Get("/api/join/{token}", handleGetShareInfo(app))
		r.Post("/api/join/{token}", handleJoinLog(app))

		// SQL query feature
		r.Post("/api/sql/execute", handleExecuteSQL(app))
		r.Get("/api/sql/schema", handleGetSQLSchema(app))
		r.Get("/api/sql/saved", handleListSavedQueries(app))
		r.Post("/api/sql/saved", handleCreateSavedQuery(app))
		r.Put("/api/sql/saved/{id}", handleUpdateSavedQuery(app))
		r.Delete("/api/sql/saved/{id}", handleDeleteSavedQuery(app))
	})

	logger.Info("Starting server", "address", cfg.ListenAddress(), "registration", cfg.AllowRegistration)
	return http.ListenAndServe(cfg.ListenAddress(), r)
}

func handleHealth(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		err := pool.QueryRow(r.Context(), "select 1").Scan(new(int))
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{
				"status": "error",
			})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"status": "ok",
		})
	}
}

func handleSettings(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"allow_registration": cfg.AllowRegistration,
			"passkeys_enabled":   cfg.PasskeysEnabled(),
		})
	}
}
