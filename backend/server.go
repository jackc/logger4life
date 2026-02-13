package backend

import (
	"context"
	"fmt"
	"log"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"
)

var serverCmd = &cobra.Command{
	Use:   "server",
	Short: "Start the HTTP server",
	RunE:  runServer,
}

var configFile string
var allowRegistration bool

func init() {
	rootCmd.AddCommand(serverCmd)
	serverCmd.Flags().StringVar(&configFile, "config", "", "path to configuration file")
	serverCmd.Flags().BoolVar(&allowRegistration, "allow-registration", false, "allow new user registration")
}

func runServer(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	var cfg Config
	if configFile != "" {
		var err error
		cfg, err = LoadConfigFile(configFile)
		if err != nil {
			return fmt.Errorf("unable to load config: %w", err)
		}
	} else {
		cfg = DefaultConfig()
	}

	if cmd.Flags().Changed("allow-registration") {
		cfg.AllowRegistration = allowRegistration
	}

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("unable to connect to database: %w", err)
	}
	defer pool.Close()

	var greeting string
	err = pool.QueryRow(ctx, "select 'Hello, World!'").Scan(&greeting)
	if err != nil {
		return fmt.Errorf("unable to query database: %w", err)
	}
	log.Printf("Database connected: %s", greeting)

	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(loadSession(pool))

	// Public routes
	r.Get("/api/hello", handleHello(pool))
	r.Get("/api/settings", handleSettings(cfg))
	r.Post("/api/register", handleRegister(pool, cfg.AllowRegistration))
	r.Post("/api/login", handleLogin(pool))

	// Protected routes
	r.Group(func(r chi.Router) {
		r.Use(requireAuth)
		r.Post("/api/logout", handleLogout(pool))
		r.Get("/api/me", handleMe)

		// Logs
		r.Post("/api/logs", handleCreateLog(pool))
		r.Get("/api/logs", handleListLogs(pool))
		r.Get("/api/logs/{logID}", handleGetLog(pool))
		r.Delete("/api/logs/{logID}", handleDeleteLog(pool))

		// Log entries
		r.Post("/api/logs/{logID}/entries", handleCreateLogEntry(pool))
		r.Get("/api/logs/{logID}/entries", handleListLogEntries(pool))
		r.Put("/api/logs/{logID}/entries/{entryID}", handleUpdateLogEntry(pool))
		r.Delete("/api/logs/{logID}/entries/{entryID}", handleDeleteLogEntry(pool))

		// Sharing
		r.Post("/api/logs/{logID}/share-token", handleCreateShareToken(pool))
		r.Delete("/api/logs/{logID}/share-token", handleDeleteShareToken(pool))
		r.Get("/api/logs/{logID}/shares", handleListShares(pool))
		r.Delete("/api/logs/{logID}/shares/{shareID}", handleRemoveShare(pool))
		r.Get("/api/join/{token}", handleGetShareInfo(pool))
		r.Post("/api/join/{token}", handleJoinLog(pool))
	})

	log.Printf("Starting server on %s (registration: %v)", cfg.ListenAddress, cfg.AllowRegistration)
	return http.ListenAndServe(cfg.ListenAddress, r)
}

func handleSettings(cfg Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"allow_registration": cfg.AllowRegistration,
		})
	}
}
