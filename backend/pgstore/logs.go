// Package pgstore implements core's driven ports with PostgreSQL.
package pgstore

import (
	"context"
	"errors"

	"github.com/jackc/logger4life/backend/core"
	"github.com/jackc/logger4life/backend/domain"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func (s *Store) CreateLog(ctx context.Context, userID, name string, fields []domain.FieldDefinition) (core.Log, error) {
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return core.Log{}, e
	}
	defer tx.Rollback(ctx)
	var l core.Log
	e = tx.QueryRow(ctx, `INSERT INTO logs(user_id,name,fields) VALUES($1,$2,$3) RETURNING id,name,fields,created_at,updated_at`, userID, name, fields).Scan(&l.ID, &l.Name, &l.Fields, &l.CreatedAt, &l.UpdatedAt)
	if e != nil {
		var pe *pgconn.PgError
		if errors.As(e, &pe) && pe.Code == "23505" {
			return core.Log{}, core.ErrLogNameTaken
		}
		return core.Log{}, e
	}
	e = tx.QueryRow(ctx, `INSERT INTO user_log_placements(user_id,log_id,folder_id,position,pinned_to_home,home_position) SELECT $1,$2,NULL,COALESCE(max(position) FILTER(WHERE folder_id IS NULL)+1,0),true,COALESCE(max(home_position) FILTER(WHERE pinned_to_home)+1,0) FROM user_log_placements WHERE user_id=$1 RETURNING position,pinned_to_home,home_position`, userID, l.ID).Scan(&l.Position, &l.PinnedToHome, &l.HomePosition)
	if e != nil {
		return core.Log{}, e
	}
	if e = tx.Commit(ctx); e != nil {
		return core.Log{}, e
	}
	l.IsOwner = true
	if l.Fields == nil {
		l.Fields = []domain.FieldDefinition{}
	}
	return l, nil
}

func (s *Store) ListLogs(ctx context.Context, userID string) ([]core.Log, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT l.id, l.name, l.fields, l.user_id = $1,
		       p.folder_id, p.position, p.pinned_to_home, p.home_position,
		       l.created_at, l.updated_at
		FROM logs l
		JOIN user_log_placements p ON p.log_id = l.id AND p.user_id = $1
		WHERE l.user_id = $1
		   OR EXISTS (SELECT 1 FROM log_shares ls WHERE ls.log_id = l.id AND ls.user_id = $1)
		ORDER BY p.folder_id NULLS FIRST, p.position`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	logs := []core.Log{}
	for rows.Next() {
		var l core.Log
		if err := rows.Scan(&l.ID, &l.Name, &l.Fields, &l.IsOwner, &l.FolderID, &l.Position, &l.PinnedToHome, &l.HomePosition, &l.CreatedAt, &l.UpdatedAt); err != nil {
			return nil, err
		}
		logs = append(logs, l)
	}
	return logs, rows.Err()
}
