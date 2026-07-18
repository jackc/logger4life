// Package pgstore implements core's driven ports with PostgreSQL.
package pgstore

import (
	"context"

	"github.com/jackc/logger4life/backend/core"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

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
