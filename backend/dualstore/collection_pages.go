package dualstore

import (
	"context"

	"github.com/jackc/logger4life/backend/core"
)

func (s *Store) ListLogsPage(ctx context.Context, user, name, id string, limit int) ([]core.LogSummary, error) {
	return compareCall("ListLogsPage", func() ([]core.LogSummary, error) { return s.primary.ListLogsPage(ctx, user, name, id, limit) }, func() ([]core.LogSummary, error) { return s.secondary.ListLogsPage(ctx, user, name, id, limit) })
}
func (s *Store) ListSavedQueriesPage(ctx context.Context, user, name, id string, limit int) ([]core.SavedQuerySummary, error) {
	return compareCall("ListSavedQueriesPage", func() ([]core.SavedQuerySummary, error) {
		return s.primary.ListSavedQueriesPage(ctx, user, name, id, limit)
	}, func() ([]core.SavedQuerySummary, error) {
		return s.secondary.ListSavedQueriesPage(ctx, user, name, id, limit)
	})
}
