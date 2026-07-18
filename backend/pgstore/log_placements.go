package pgstore

import (
	"context"
	"errors"

	"github.com/jackc/logger4life/backend/core"
	"github.com/jackc/logger4life/backend/domain"
	"github.com/jackc/pgx/v5"
)

func (s *Store) UpdateLogPlacement(ctx context.Context, userID, logID string, ch domain.LogPlacementChange) error {
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	var oldFolder *string
	var oldPos int
	e = tx.QueryRow(ctx, `SELECT folder_id,position FROM user_log_placements WHERE user_id=$1 AND log_id=$2 FOR UPDATE`, userID, logID).Scan(&oldFolder, &oldPos)
	if errors.Is(e, pgx.ErrNoRows) {
		return core.ErrLogNotFound
	}
	if e != nil {
		return e
	}
	if ch.FolderID != nil {
		e = folderOwned(ctx, tx, *ch.FolderID, userID)
		if errors.Is(e, pgx.ErrNoRows) {
			return core.ErrFolderNotFound
		}
		if e != nil {
			return e
		}
	}
	same := (oldFolder == nil && ch.FolderID == nil) || (oldFolder != nil && ch.FolderID != nil && *oldFolder == *ch.FolderID)
	if same {
		var n int
		if e = tx.QueryRow(ctx, `SELECT count(*) FROM user_log_placements WHERE user_id=$1 AND folder_id IS NOT DISTINCT FROM $2`, userID, oldFolder).Scan(&n); e != nil {
			return e
		}
		if ch.Position > n-1 {
			ch.Position = n - 1
		}
		if ch.Position > oldPos {
			_, e = tx.Exec(ctx, `UPDATE user_log_placements SET position=position-1 WHERE user_id=$1 AND folder_id IS NOT DISTINCT FROM $2 AND position>$3 AND position<=$4`, userID, oldFolder, oldPos, ch.Position)
		} else if ch.Position < oldPos {
			_, e = tx.Exec(ctx, `UPDATE user_log_placements SET position=position+1 WHERE user_id=$1 AND folder_id IS NOT DISTINCT FROM $2 AND position>=$3 AND position<$4`, userID, oldFolder, ch.Position, oldPos)
		}
		if e != nil {
			return e
		}
		_, e = tx.Exec(ctx, `UPDATE user_log_placements SET position=$1,updated_at=now() WHERE user_id=$2 AND log_id=$3`, ch.Position, userID, logID)
	} else {
		var n int
		if e = tx.QueryRow(ctx, `SELECT count(*) FROM user_log_placements WHERE user_id=$1 AND folder_id IS NOT DISTINCT FROM $2`, userID, ch.FolderID).Scan(&n); e != nil {
			return e
		}
		if ch.Position > n {
			ch.Position = n
		}
		if _, e = tx.Exec(ctx, `UPDATE user_log_placements SET position=position-1 WHERE user_id=$1 AND folder_id IS NOT DISTINCT FROM $2 AND position>$3`, userID, oldFolder, oldPos); e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, `UPDATE user_log_placements SET position=position+1 WHERE user_id=$1 AND folder_id IS NOT DISTINCT FROM $2 AND position>=$3`, userID, ch.FolderID, ch.Position); e != nil {
			return e
		}
		_, e = tx.Exec(ctx, `UPDATE user_log_placements SET folder_id=$1,position=$2,updated_at=now() WHERE user_id=$3 AND log_id=$4`, ch.FolderID, ch.Position, userID, logID)
	}
	if e != nil {
		return e
	}
	return tx.Commit(ctx)
}
func (s *Store) PinLog(ctx context.Context, userID, logID string, change domain.HomePinChange) error {
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	var current bool
	e = tx.QueryRow(ctx, `SELECT pinned_to_home FROM user_log_placements WHERE user_id=$1 AND log_id=$2 FOR UPDATE`, userID, logID).Scan(&current)
	if errors.Is(e, pgx.ErrNoRows) {
		return core.ErrLogNotFound
	}
	if e != nil {
		return e
	}
	if current == change.Pinned {
		return tx.Commit(ctx)
	}
	if change.Pinned {
		_, e = tx.Exec(ctx, `UPDATE user_log_placements SET pinned_to_home=true,home_position=COALESCE((SELECT max(home_position)+1 FROM user_log_placements WHERE user_id=$1 AND pinned_to_home),0),updated_at=now() WHERE user_id=$1 AND log_id=$2`, userID, logID)
	} else {
		_, e = tx.Exec(ctx, `UPDATE user_log_placements SET pinned_to_home=false,updated_at=now() WHERE user_id=$1 AND log_id=$2`, userID, logID)
	}
	if e != nil {
		return e
	}
	return tx.Commit(ctx)
}
func (s *Store) UpdateLogHomePosition(ctx context.Context, userID, logID string, change domain.HomeOrderChange) error {
	pos := change.HomePosition
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	var pinned bool
	var old int
	e = tx.QueryRow(ctx, `SELECT pinned_to_home,home_position FROM user_log_placements WHERE user_id=$1 AND log_id=$2 FOR UPDATE`, userID, logID).Scan(&pinned, &old)
	if errors.Is(e, pgx.ErrNoRows) {
		return core.ErrLogNotFound
	}
	if e != nil {
		return e
	}
	if !pinned {
		return core.ErrLogNotPinned
	}
	var n int
	if e = tx.QueryRow(ctx, `SELECT count(*) FROM user_log_placements WHERE user_id=$1 AND pinned_to_home`, userID).Scan(&n); e != nil {
		return e
	}
	if pos > n-1 {
		pos = n - 1
	}
	if pos == old {
		return tx.Commit(ctx)
	}
	if pos > old {
		_, e = tx.Exec(ctx, `UPDATE user_log_placements SET home_position=home_position-1 WHERE user_id=$1 AND pinned_to_home AND home_position>$2 AND home_position<=$3`, userID, old, pos)
	} else {
		_, e = tx.Exec(ctx, `UPDATE user_log_placements SET home_position=home_position+1 WHERE user_id=$1 AND pinned_to_home AND home_position>=$2 AND home_position<$3`, userID, pos, old)
	}
	if e != nil {
		return e
	}
	_, e = tx.Exec(ctx, `UPDATE user_log_placements SET home_position=$1,updated_at=now() WHERE user_id=$2 AND log_id=$3`, pos, userID, logID)
	if e != nil {
		return e
	}
	return tx.Commit(ctx)
}
