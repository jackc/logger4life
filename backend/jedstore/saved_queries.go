package jedstore

import (
	"context"
	"errors"
	"github.com/jackc/logger4life/backend/core"
)

func scanSaved(row rowScanner) (core.SavedQuery, error) {
	var q core.SavedQuery
	e := row.Scan(&q.ID, &q.Name, &q.QueryText, &q.CreatedAt, &q.UpdatedAt)
	return q, e
}
func (s *Store) ListSavedQueries(ctx context.Context, user string) ([]core.SavedQuery, error) {
	rows, e := s.conn(ctx).Query(ctx, `SELECT id,name,query_text,created_at,updated_at FROM saved_sql_queries WHERE user_id=$1 ORDER BY lower(name)`, user)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []core.SavedQuery{}
	for rows.Next() {
		q, e := scanSaved(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, q)
	}
	return out, rows.Err()
}
func (s *Store) GetSavedQueryByName(ctx context.Context, user, name string) (core.SavedQuery, error) {
	q, e := scanSaved(s.conn(ctx).QueryRow(ctx, `SELECT id,name,query_text,created_at,updated_at FROM saved_sql_queries WHERE user_id=$1 AND name=$2`, user, name))
	if errors.Is(e, errNoRows) {
		e = core.ErrSavedQueryNotFound
	}
	return q, e
}
func savedErr(e error) error {
	if errors.Is(e, errNoRows) {
		return core.ErrSavedQueryNotFound
	}
	if uniqueViolation(e, "saved_sql_queries_user_id_name") {
		return core.ErrSavedQueryNameTaken
	}
	return e
}
func (s *Store) CreateSavedQuery(ctx context.Context, id, user, name, text string) (core.SavedQuery, error) {
	q, e := scanSaved(s.conn(ctx).QueryRow(ctx, `INSERT INTO saved_sql_queries(id,user_id,name,query_text) VALUES($1,$2,$3,$4) RETURNING id,name,query_text,created_at,updated_at`, id, user, name, text))
	return q, savedErr(e)
}
func (s *Store) UpdateSavedQuery(ctx context.Context, user, id, name, text string) (core.SavedQuery, error) {
	q, e := scanSaved(s.conn(ctx).QueryRow(ctx, `UPDATE saved_sql_queries SET name=$1,query_text=$2,updated_at=now() WHERE id=$3 AND user_id=$4 RETURNING id,name,query_text,created_at,updated_at`, name, text, id, user))
	return q, savedErr(e)
}
func (s *Store) DeleteSavedQuery(ctx context.Context, user, id string) error {
	tag, e := s.conn(ctx).Exec(ctx, `DELETE FROM saved_sql_queries WHERE id=$1 AND user_id=$2`, id, user)
	if e == nil && tag.RowsAffected() == 0 {
		return core.ErrSavedQueryNotFound
	}
	return e
}
