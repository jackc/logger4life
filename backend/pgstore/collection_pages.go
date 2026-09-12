package pgstore

import (
	"context"

	"github.com/jackc/logger4life/backend/core"
)

func (s *Store) ListLogsPage(ctx context.Context, user, name, id string, limit int) ([]core.LogSummary, error) {
	rows, err := s.conn(ctx).Query(ctx, `SELECT l.id,l.name,l.fields,l.user_id=$1,l.created_at,l.updated_at,lower(l.name)
	 FROM logs l WHERE (`+visibleToUser+`) AND ($3::text=''::text OR lower(l.name) COLLATE "C" > $2::text COLLATE "C"
	 OR (lower(l.name) COLLATE "C" = $2::text COLLATE "C" AND l.id::text COLLATE "C" > $3::text COLLATE "C"))
	 ORDER BY lower(l.name) COLLATE "C", l.id::text COLLATE "C" LIMIT $4`, user, name, id, min(max(limit, 1), 101))
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
	rows, err := s.conn(ctx).Query(ctx, `SELECT id,name,query_text,created_at,updated_at,lower(name)
	 FROM saved_sql_queries WHERE user_id=$1 AND ($3::text=''::text OR lower(name) COLLATE "C" > $2::text COLLATE "C"
	 OR (lower(name) COLLATE "C" = $2::text COLLATE "C" AND id::text COLLATE "C" > $3::text COLLATE "C"))
	 ORDER BY lower(name) COLLATE "C", id::text COLLATE "C" LIMIT $4`, user, name, id, min(max(limit, 1), 101))
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
