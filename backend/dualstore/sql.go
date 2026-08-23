package dualstore

import (
	"context"

	"github.com/jackc/logger4life/backend/core"
)

func (s *Store) ListSQLSchemaViews(ctx context.Context) ([]*core.SQLSchemaView, error) {
	return compareCall("ListSQLSchemaViews",
		func() ([]*core.SQLSchemaView, error) { return s.primary.ListSQLSchemaViews(ctx) },
		func() ([]*core.SQLSchemaView, error) { return s.secondary.ListSQLSchemaViews(ctx) })
}

func (s *Store) ExecuteUserSQL(ctx context.Context, userID, query string) (core.UserSQLResult, error) {
	return compareCall("ExecuteUserSQL",
		func() (core.UserSQLResult, error) { return s.primary.ExecuteUserSQL(ctx, userID, query) },
		func() (core.UserSQLResult, error) { return s.secondary.ExecuteUserSQL(ctx, userID, query) })
}
