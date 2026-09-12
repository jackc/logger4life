package jedstore

import (
	"context"
	"strconv"

	"github.com/jackc/logger4life/backend/core"
)

// jed requires a literal LIMIT. Only a clamped integer is interpolated;
// user identity and cursor values remain bound parameters.
func (s *Store) ListLogsPage(ctx context.Context, user, name, id string, limit int) ([]core.LogSummary, error) {
	args := []any{user}
	cursorFilter := ""
	if id != "" {
		cursorFilter = ` AND (lower(l.name) COLLATE "C" > $2 OR (lower(l.name) COLLATE "C" = $2 AND l.id > $3))`
		args = append(args, name, id)
	}
	rows, err := s.conn(ctx).Query(ctx, `SELECT l.id,l.name,l.fields,l.user_id=$1,l.created_at,l.updated_at,lower(l.name)
	 FROM all_logs l WHERE (`+visibleToUser+`)`+cursorFilter+`
	 ORDER BY lower(l.name) COLLATE "C", l.id LIMIT `+strconv.Itoa(min(max(limit, 1), 101)), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []core.LogSummary{}
	for rows.Next() {
		var l core.LogSummary
		if err := rows.Scan(&l.ID, &l.Name, &l.Fields, &l.IsOwner, &l.CreatedAt, &l.UpdatedAt, &l.SortName); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func (s *Store) ListSavedQueriesPage(ctx context.Context, user, name, id string, limit int) ([]core.SavedQuerySummary, error) {
	args := []any{user}
	cursorFilter := ""
	if id != "" {
		cursorFilter = ` AND (lower(name) COLLATE "C" > $2 OR (lower(name) COLLATE "C" = $2 AND id > $3))`
		args = append(args, name, id)
	}
	rows, err := s.conn(ctx).Query(ctx, `SELECT id,name,query_text,created_at,updated_at,lower(name)
	 FROM saved_sql_queries WHERE user_id=$1`+cursorFilter+`
	 ORDER BY lower(name) COLLATE "C", id LIMIT `+strconv.Itoa(min(max(limit, 1), 101)), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []core.SavedQuerySummary{}
	for rows.Next() {
		var q core.SavedQuerySummary
		if err := rows.Scan(&q.ID, &q.Name, &q.QueryText, &q.CreatedAt, &q.UpdatedAt, &q.SortName); err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	return out, rows.Err()
}
