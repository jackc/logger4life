// Package pgstore implements core's driven ports with PostgreSQL.
package pgstore

import (
	"context"
	"encoding/hex"
	"errors"

	"github.com/jackc/logger4life/backend/core"
	"github.com/jackc/logger4life/backend/domain"
	"github.com/jackc/pgx/v5"
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

func (s *Store) GetLog(ctx context.Context, userID, logID string) (core.Log, error) {
	var l core.Log
	var token []byte
	e := s.pool.QueryRow(ctx, `SELECT l.id,l.name,l.fields,l.user_id=$1,l.share_token,p.folder_id,p.position,p.pinned_to_home,p.home_position,l.created_at,l.updated_at FROM logs l JOIN user_log_placements p ON p.log_id=l.id AND p.user_id=$1 WHERE l.id=$2`, userID, logID).Scan(&l.ID, &l.Name, &l.Fields, &l.IsOwner, &token, &l.FolderID, &l.Position, &l.PinnedToHome, &l.HomePosition, &l.CreatedAt, &l.UpdatedAt)
	if errors.Is(e, pgx.ErrNoRows) {
		return core.Log{}, core.ErrLogNotFound
	}
	if e != nil {
		return core.Log{}, e
	}
	if l.IsOwner && token != nil {
		v := hex.EncodeToString(token)
		l.ShareToken = &v
	}
	if l.Fields == nil {
		l.Fields = []domain.FieldDefinition{}
	}
	return l, nil
}
func (s *Store) UpdateLog(ctx context.Context, userID, logID, name string, fields []domain.FieldDefinition) (core.Log, error) {
	var l core.Log
	var token []byte
	e := s.pool.QueryRow(ctx, `WITH updated AS (UPDATE logs SET name=$1,fields=$2,updated_at=now() WHERE id=$3 AND user_id=$4 RETURNING id,name,fields,share_token,created_at,updated_at) SELECT u.id,u.name,u.fields,u.share_token,p.folder_id,p.position,p.pinned_to_home,p.home_position,u.created_at,u.updated_at FROM updated u JOIN user_log_placements p ON p.log_id=u.id AND p.user_id=$4`, name, fields, logID, userID).Scan(&l.ID, &l.Name, &l.Fields, &token, &l.FolderID, &l.Position, &l.PinnedToHome, &l.HomePosition, &l.CreatedAt, &l.UpdatedAt)
	if errors.Is(e, pgx.ErrNoRows) {
		return core.Log{}, core.ErrLogNotFound
	}
	if e != nil {
		var pe *pgconn.PgError
		if errors.As(e, &pe) && pe.Code == "23505" {
			return core.Log{}, core.ErrLogNameTaken
		}
		return core.Log{}, e
	}
	l.IsOwner = true
	if token != nil {
		v := hex.EncodeToString(token)
		l.ShareToken = &v
	}
	return l, nil
}
func (s *Store) DeleteLog(ctx context.Context, userID, logID string) error {
	tag, e := s.pool.Exec(ctx, `DELETE FROM logs WHERE id=$1 AND user_id=$2`, logID, userID)
	if e == nil && tag.RowsAffected() == 0 {
		return core.ErrLogNotFound
	}
	return e
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
