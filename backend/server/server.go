package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/jackc/logger4life/backend/cimd"
	"github.com/jackc/logger4life/backend/core"
	"github.com/jackc/logger4life/backend/dualstore"
	"github.com/jackc/logger4life/backend/jedstore"
	"github.com/jackc/logger4life/backend/pgstore"
	"github.com/jackc/pgx/v5/pgxpool"
)

// HealthCheck reports whether the database answers queries. The health endpoint
// is an infrastructure liveness probe, not a catalog operation: it reads no
// application state, so the composition root supplies this function and the
// handler holds no SQL or connection pool.
type HealthCheck func(ctx context.Context) error

// Run connects the database, wires core to its ports, and serves the HTTP
// API until the process exits. The composition root lives here: one store and
// one core.Core, injected into every adapter.
func Run(ctx context.Context, cfg Config) error {
	if err := cfg.normalizeMCPCanonicalURL(); err != nil {
		return err
	}
	secureCookies = cfg.SecureCookies

	handler, err := cfg.SlogHandler()
	if err != nil {
		return fmt.Errorf("unable to initialize logger: %w", err)
	}
	logger := slog.New(handler)

	app, health, cleanup, err := BuildBackend(ctx, cfg, logger)
	if err != nil {
		return err
	}
	defer cleanup()

	routes := newHTTPRegistry(app, health, cfg, logger, nil)
	if cfg.MCPEnabled() {
		defer startOAuthClientCleanup(ctx, app, cfg, logger)()
		logger.Info("MCP enabled", "canonical_url", cfg.MCPCanonicalURL)
	}

	logger.Info("Starting server", "address", cfg.ListenAddress(), "backend", cfg.DatabaseBackend, "registration", cfg.AllowRegistration)
	return http.ListenAndServe(cfg.ListenAddress(), routes.Handler())
}

type persistence interface {
	core.UserStore
	core.SessionStore
	core.PasskeyStore
	core.PasskeyChallengeStore
	core.LogStore
	core.CollectionStore
	core.LogEntryStore
	core.LogPlacementStore
	core.FolderStore
	core.SavedQueryStore
	core.SQLSchemaStore
	core.UserSQLExecutor
	core.SharingStore
	core.OAuthStore
	core.Transactor
}

// BuildBackend opens the configured persistence backend and wires every port
// into one core. It is separate from Run so integration tests and future CLI
// commands can exercise the same composition root.
func BuildBackend(ctx context.Context, cfg Config, logger *slog.Logger) (*core.Core, HealthCheck, func(), error) {
	return buildBackend(ctx, cfg, logger, cimd.New())
}

func buildBackend(ctx context.Context, cfg Config, logger *slog.Logger, metadata core.OAuthClientMetadataResolver) (*core.Core, HealthCheck, func(), error) {
	noop := func() {}
	if err := cfg.normalizeMCPCanonicalURL(); err != nil {
		return nil, nil, noop, err
	}
	if err := cfg.validateLimits(); err != nil {
		return nil, nil, noop, err
	}
	var wan *webauthn.WebAuthn
	var err error
	if cfg.PasskeysEnabled() {
		wan, err = webauthn.New(&webauthn.Config{
			RPDisplayName: "Logger4Life",
			RPID:          cfg.WebAuthnRPID,
			RPOrigins:     []string{cfg.WebAuthnOrigin},
		})
		if err != nil {
			return nil, nil, noop, fmt.Errorf("unable to initialize webauthn: %w", err)
		}
	}

	buildCore := func(store persistence) *core.Core {
		return core.New(core.Config{
			Collections: store, Users: store, Sessions: store, Passkeys: store, Challenges: store, WebAuthn: wan, Tx: store, Logs: store, Entries: store, Placements: store, Folders: store, SavedQueries: store, SQLSchema: store, UserSQL: store, SQLConcurrencyPerUser: cfg.SQLConcurrencyPerUser, SQLConcurrencyGlobal: cfg.SQLConcurrencyGlobal, Sharing: store, OAuth: store, OAuthMetadata: metadata, OAuthIssuer: cfg.MCPCanonicalURL, OAuthMaxClients: cfg.OAuthMaxClients,
			// RequireUser is outermost so an anonymous caller is turned away
			// before the audit trail records an attempt it never let through.
			Middleware: []core.Middleware{core.RequireUser(), auditMiddleware(logger)},
		})
	}

	switch cfg.DatabaseBackend {
	case "", "postgresql":
		pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
		if err != nil {
			return nil, nil, noop, fmt.Errorf("unable to connect to PostgreSQL: %w", err)
		}
		health := func(ctx context.Context) error {
			return pool.QueryRow(ctx, "select 1").Scan(new(int))
		}
		if err := health(ctx); err != nil {
			pool.Close()
			return nil, nil, noop, fmt.Errorf("unable to query PostgreSQL: %w", err)
		}
		logger.Info("PostgreSQL connected")
		return buildCore(pgstore.New(pool)), health, pool.Close, nil

	case "jed":
		if cfg.JedDataDir == "" {
			return nil, nil, noop, errors.New("JED_DATA_DIR is required for the jed backend")
		}
		store, err := jedstore.Open(cfg.JedDataDir)
		if err != nil {
			return nil, nil, noop, err
		}
		logger.Info("jed database opened", "data_dir", cfg.JedDataDir)
		return buildCore(store), store.Ping, func() { _ = store.Close() }, nil

	case "both":
		if cfg.JedDataDir == "" {
			return nil, nil, noop, errors.New("JED_DATA_DIR is required for the both backend")
		}
		pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
		if err != nil {
			return nil, nil, noop, fmt.Errorf("unable to connect to PostgreSQL: %w", err)
		}
		pgHealth := func(ctx context.Context) error {
			return pool.QueryRow(ctx, "select 1").Scan(new(int))
		}
		if err := pgHealth(ctx); err != nil {
			pool.Close()
			return nil, nil, noop, fmt.Errorf("unable to query PostgreSQL: %w", err)
		}
		jedStore, err := jedstore.Open(cfg.JedDataDir)
		if err != nil {
			pool.Close()
			return nil, nil, noop, err
		}
		store := dualstore.New(pgstore.New(pool), jedStore)
		health := func(ctx context.Context) error {
			if err := pgHealth(ctx); err != nil {
				return err
			}
			return jedStore.Ping(ctx)
		}
		cleanup := func() {
			_ = jedStore.Close()
			pool.Close()
		}
		logger.Info("PostgreSQL and jed connected in comparison mode", "data_dir", cfg.JedDataDir)
		return buildCore(store), health, cleanup, nil

	default:
		return nil, nil, noop, fmt.Errorf("unknown DATABASE_BACKEND %q (want postgresql, jed, or both)", cfg.DatabaseBackend)
	}
}

func handleHealth(health HealthCheck) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := health(r.Context()); err != nil {
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
