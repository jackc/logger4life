package backend

import (
	"context"
	"encoding/json"
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
var staticURL string

func init() {
	rootCmd.AddCommand(serverCmd)
	serverCmd.Flags().StringVar(&configFile, "config", "", "path to configuration file")
	serverCmd.Flags().StringVar(&staticURL, "static-url", "", "URL for static assets (SvelteKit dev server)")
}

func runServer(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, "postgres://postgres:postgres@localhost:5432/logger4life_dev")
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

	r.Get("/api/hello", func(w http.ResponseWriter, r *http.Request) {
		var msg string
		err := pool.QueryRow(r.Context(), "select 'Hello, World!'").Scan(&msg)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"message": msg})
	})

	log.Printf("Starting server on :4000 (static-url: %s)", staticURL)
	return http.ListenAndServe(":4000", r)
}
