package dualstore

import (
	"context"

	"github.com/jackc/logger4life/backend/core"
)

func (s *Store) ListSavedQueries(ctx context.Context, userID string) ([]core.SavedQuery, error) {
	return compareCall("ListSavedQueries",
		func() ([]core.SavedQuery, error) { return s.primary.ListSavedQueries(ctx, userID) },
		func() ([]core.SavedQuery, error) { return s.secondary.ListSavedQueries(ctx, userID) })
}

func (s *Store) GetSavedQueryByName(ctx context.Context, userID, name string) (core.SavedQuery, error) {
	return compareCall("GetSavedQueryByName",
		func() (core.SavedQuery, error) { return s.primary.GetSavedQueryByName(ctx, userID, name) },
		func() (core.SavedQuery, error) { return s.secondary.GetSavedQueryByName(ctx, userID, name) })
}

func (s *Store) CreateSavedQuery(ctx context.Context, id, userID, name, query string) (core.SavedQuery, error) {
	return compareCall("CreateSavedQuery",
		func() (core.SavedQuery, error) { return s.primary.CreateSavedQuery(ctx, id, userID, name, query) },
		func() (core.SavedQuery, error) { return s.secondary.CreateSavedQuery(ctx, id, userID, name, query) })
}

func (s *Store) UpdateSavedQuery(ctx context.Context, userID, id, name, query string) (core.SavedQuery, error) {
	return compareCall("UpdateSavedQuery",
		func() (core.SavedQuery, error) { return s.primary.UpdateSavedQuery(ctx, userID, id, name, query) },
		func() (core.SavedQuery, error) { return s.secondary.UpdateSavedQuery(ctx, userID, id, name, query) })
}

func (s *Store) DeleteSavedQuery(ctx context.Context, userID, id string) error {
	return compareError("DeleteSavedQuery",
		func() error { return s.primary.DeleteSavedQuery(ctx, userID, id) },
		func() error { return s.secondary.DeleteSavedQuery(ctx, userID, id) })
}
