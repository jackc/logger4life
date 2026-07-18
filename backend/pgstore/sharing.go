package pgstore

import (
	"context"
	"errors"

	"github.com/jackc/logger4life/backend/core"
	"github.com/jackc/pgx/v5"
)

func (s *Store) CreateShareToken(ctx context.Context, userID, logID string, token []byte) error {
	tag, err := s.pool.Exec(ctx, `UPDATE logs SET share_token=$1 WHERE id=$2 AND user_id=$3`, token, logID, userID)
	if err == nil && tag.RowsAffected() == 0 {
		return core.ErrLogNotFound
	}
	return err
}

func (s *Store) DeleteShareToken(ctx context.Context, userID, logID string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE logs SET share_token=NULL WHERE id=$1 AND user_id=$2`, logID, userID)
	if err == nil && tag.RowsAffected() == 0 {
		return core.ErrLogNotFound
	}
	return err
}

func ownedLog(ctx context.Context, q queryRower, userID, logID string) error {
	var exists bool
	err := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM logs WHERE id=$1 AND user_id=$2)`, logID, userID).Scan(&exists)
	if err != nil {
		return err
	}
	if !exists {
		return core.ErrLogNotFound
	}
	return nil
}

func (s *Store) ListSharedUsers(ctx context.Context, userID, logID string) ([]core.SharedUser, error) {
	if err := ownedLog(ctx, s.pool, userID, logID); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT ls.id,u.username,ls.created_at
		FROM log_shares ls
		JOIN users u ON u.id=ls.user_id
		WHERE ls.log_id=$1
		ORDER BY ls.created_at`, logID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	shares := []core.SharedUser{}
	for rows.Next() {
		var share core.SharedUser
		if err := rows.Scan(&share.ID, &share.Username, &share.SharedAt); err != nil {
			return nil, err
		}
		shares = append(shares, share)
	}
	return shares, rows.Err()
}

func (s *Store) RemoveSharedUser(ctx context.Context, userID, logID, shareID string) error {
	if err := ownedLog(ctx, s.pool, userID, logID); err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx, `DELETE FROM log_shares WHERE id=$1 AND log_id=$2`, shareID, logID)
	if err == nil && tag.RowsAffected() == 0 {
		return core.ErrShareNotFound
	}
	return err
}

func (s *Store) GetShareInfo(ctx context.Context, userID string, token []byte) (core.ShareInfo, error) {
	var info core.ShareInfo
	err := s.pool.QueryRow(ctx, `
		SELECT l.id,l.name,u.username,l.user_id=$1,
		       EXISTS(SELECT 1 FROM log_shares ls WHERE ls.log_id=l.id AND ls.user_id=$1)
		FROM logs l
		JOIN users u ON u.id=l.user_id
		WHERE l.share_token=$2`, userID, token).Scan(&info.LogID, &info.LogName, &info.OwnerUsername, &info.IsOwner, &info.AlreadyMember)
	if errors.Is(err, pgx.ErrNoRows) {
		return core.ShareInfo{}, core.ErrInvalidShareLink
	}
	if err != nil {
		return core.ShareInfo{}, err
	}
	if info.IsOwner {
		info.AlreadyMember = false
	}
	return info, nil
}

func (s *Store) JoinSharedLog(ctx context.Context, userID string, token []byte) (core.JoinSharedLogResult, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return core.JoinSharedLogResult{}, err
	}
	defer tx.Rollback(ctx)

	var result core.JoinSharedLogResult
	var ownerID string
	err = tx.QueryRow(ctx, `SELECT id,name,user_id FROM logs WHERE share_token=$1 FOR SHARE`, token).Scan(&result.LogID, &result.LogName, &ownerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return core.JoinSharedLogResult{}, core.ErrInvalidShareLink
	}
	if err != nil {
		return core.JoinSharedLogResult{}, err
	}
	if ownerID == userID {
		return core.JoinSharedLogResult{}, core.ErrAlreadyOwnLog
	}

	var shareID string
	err = tx.QueryRow(ctx, `
		INSERT INTO log_shares(log_id,user_id) VALUES($1,$2)
		ON CONFLICT (log_id,user_id) DO NOTHING
		RETURNING id`, result.LogID, userID).Scan(&shareID)
	if errors.Is(err, pgx.ErrNoRows) {
		result.AlreadyMember = true
		if err := tx.Commit(ctx); err != nil {
			return core.JoinSharedLogResult{}, err
		}
		return result, nil
	}
	if err != nil {
		return core.JoinSharedLogResult{}, err
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO user_log_placements(user_id,log_id,folder_id,position,pinned_to_home,home_position)
		SELECT $1,$2,NULL,
		       COALESCE(max(position) FILTER (WHERE folder_id IS NULL)+1,0),
		       true,
		       COALESCE(max(home_position) FILTER (WHERE pinned_to_home)+1,0)
		FROM user_log_placements WHERE user_id=$1
		ON CONFLICT (user_id,log_id) DO NOTHING`, userID, result.LogID)
	if err != nil {
		return core.JoinSharedLogResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return core.JoinSharedLogResult{}, err
	}
	return result, nil
}
