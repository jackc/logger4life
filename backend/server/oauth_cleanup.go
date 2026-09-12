package server

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/logger4life/backend/core"
)

func startOAuthClientCleanup(ctx context.Context, app *core.Core, cfg Config, logger *slog.Logger) func() {
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for {
			jobCtx, stop := context.WithTimeout(ctx, 30*time.Second)
			before := time.Now().Add(-time.Duration(limitOrDefault(cfg.OAuthUnusedClientHours, 24)) * time.Hour)
			deleted, err := app.PruneUnusedOAuthClients(jobCtx, before)
			stop()
			if err != nil && ctx.Err() == nil {
				logger.Error("OAuth client cleanup failed", "error", err)
			}
			if err == nil && deleted > 0 {
				logger.Info("Removed unused OAuth clients", "count", deleted)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return func() { cancel(); <-done }
}
