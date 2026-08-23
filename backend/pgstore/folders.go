package pgstore

import (
	"context"
	"errors"

	"github.com/jackc/logger4life/backend/core"
	"github.com/jackc/pgx/v5"
)

type rowScanner interface{ Scan(...any) error }

func scanFolder(row rowScanner) (core.Folder, error) {
	var f core.Folder
	e := row.Scan(&f.ID, &f.Name, &f.ParentFolderID, &f.Position, &f.CreatedAt, &f.UpdatedAt)
	return f, e
}

type queryRower interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func folderOwned(ctx context.Context, q queryRower, folderID, userID string) error {
	var owner string
	e := q.QueryRow(ctx, `SELECT user_id FROM folders WHERE id=$1`, folderID).Scan(&owner)
	if e != nil {
		return e
	}
	if owner != userID {
		return pgx.ErrNoRows
	}
	return nil
}
func (s *Store) CreateFolder(ctx context.Context, id, userID, name string, parent *string) (core.Folder, error) {
	if parent != nil {
		e := folderOwned(ctx, s.conn(ctx), *parent, userID)
		if errors.Is(e, pgx.ErrNoRows) {
			return core.Folder{}, core.ErrParentFolderNotFound
		}
		if e != nil {
			return core.Folder{}, e
		}
	}
	return scanFolder(s.conn(ctx).QueryRow(ctx, `INSERT INTO folders(id,user_id,parent_folder_id,name,position) SELECT $1,$2,$3,$4,COALESCE(max(position)+1,0) FROM folders WHERE user_id=$2 AND parent_folder_id IS NOT DISTINCT FROM $3 RETURNING id,name,parent_folder_id,position,created_at,updated_at`, id, userID, parent, name))
}
func (s *Store) ListFolders(ctx context.Context, userID string) ([]core.Folder, error) {
	rows, e := s.conn(ctx).Query(ctx, `SELECT id,name,parent_folder_id,position,created_at,updated_at FROM folders WHERE user_id=$1 ORDER BY parent_folder_id NULLS FIRST,position`, userID)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []core.Folder{}
	for rows.Next() {
		f, e := scanFolder(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, f)
	}
	return out, rows.Err()
}
func (s *Store) RenameFolder(ctx context.Context, userID, folderID, name string) (core.Folder, error) {
	f, e := scanFolder(s.conn(ctx).QueryRow(ctx, `UPDATE folders SET name=$1,updated_at=now() WHERE id=$2 AND user_id=$3 RETURNING id,name,parent_folder_id,position,created_at,updated_at`, name, folderID, userID))
	if errors.Is(e, pgx.ErrNoRows) {
		e = core.ErrFolderNotFound
	}
	return f, e
}
func (s *Store) MoveFolder(ctx context.Context, userID, folderID string, parent *string, pos int) error {
	return s.InTx(ctx, func(ctx context.Context) error {
		tx := s.conn(ctx)
		var owner string
		var oldParent *string
		var oldPos int
		e := tx.QueryRow(ctx, `SELECT user_id,parent_folder_id,position FROM folders WHERE id=$1 FOR UPDATE`, folderID).Scan(&owner, &oldParent, &oldPos)
		if errors.Is(e, pgx.ErrNoRows) || (e == nil && owner != userID) {
			return core.ErrFolderNotFound
		}
		if e != nil {
			return e
		}
		if parent != nil {
			if *parent == folderID {
				return core.ErrFolderOwnParent
			}
			e = folderOwned(ctx, tx, *parent, userID)
			if errors.Is(e, pgx.ErrNoRows) {
				return core.ErrParentFolderNotFound
			}
			if e != nil {
				return e
			}
			var cycle bool
			e = tx.QueryRow(ctx, `WITH RECURSIVE descendants AS (SELECT id FROM folders WHERE id=$1 UNION ALL SELECT f.id FROM folders f JOIN descendants d ON f.parent_folder_id=d.id) SELECT EXISTS(SELECT 1 FROM descendants WHERE id=$2)`, folderID, *parent).Scan(&cycle)
			if e != nil {
				return e
			}
			if cycle {
				return core.ErrFolderCycle
			}
		}
		same := (oldParent == nil && parent == nil) || (oldParent != nil && parent != nil && *oldParent == *parent)
		if same {
			var count int
			if e = tx.QueryRow(ctx, `SELECT count(*) FROM folders WHERE user_id=$1 AND parent_folder_id IS NOT DISTINCT FROM $2`, userID, oldParent).Scan(&count); e != nil {
				return e
			}
			if pos > count-1 {
				pos = count - 1
			}
			if pos > oldPos {
				_, e = tx.Exec(ctx, `UPDATE folders SET position=position-1 WHERE user_id=$1 AND parent_folder_id IS NOT DISTINCT FROM $2 AND position>$3 AND position<=$4`, userID, oldParent, oldPos, pos)
			} else if pos < oldPos {
				_, e = tx.Exec(ctx, `UPDATE folders SET position=position+1 WHERE user_id=$1 AND parent_folder_id IS NOT DISTINCT FROM $2 AND position>=$3 AND position<$4`, userID, oldParent, pos, oldPos)
			}
			if e != nil {
				return e
			}
			_, e = tx.Exec(ctx, `UPDATE folders SET position=$1,updated_at=now() WHERE id=$2`, pos, folderID)
		} else {
			var count int
			if e = tx.QueryRow(ctx, `SELECT count(*) FROM folders WHERE user_id=$1 AND parent_folder_id IS NOT DISTINCT FROM $2`, userID, parent).Scan(&count); e != nil {
				return e
			}
			if pos > count {
				pos = count
			}
			if _, e = tx.Exec(ctx, `UPDATE folders SET position=position-1 WHERE user_id=$1 AND parent_folder_id IS NOT DISTINCT FROM $2 AND position>$3`, userID, oldParent, oldPos); e != nil {
				return e
			}
			if _, e = tx.Exec(ctx, `UPDATE folders SET position=position+1 WHERE user_id=$1 AND parent_folder_id IS NOT DISTINCT FROM $2 AND position>=$3`, userID, parent, pos); e != nil {
				return e
			}
			_, e = tx.Exec(ctx, `UPDATE folders SET parent_folder_id=$1,position=$2,updated_at=now() WHERE id=$3`, parent, pos, folderID)
		}
		return e
	})
}
func (s *Store) DeleteFolder(ctx context.Context, userID, folderID string) error {
	return s.InTx(ctx, func(ctx context.Context) error {
		tx := s.conn(ctx)
		var owner string
		var parent *string
		var pos int
		e := tx.QueryRow(ctx, `SELECT user_id,parent_folder_id,position FROM folders WHERE id=$1 FOR UPDATE`, folderID).Scan(&owner, &parent, &pos)
		if errors.Is(e, pgx.ErrNoRows) || (e == nil && owner != userID) {
			return core.ErrFolderNotFound
		}
		if e != nil {
			return e
		}
		var children bool
		if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM folders WHERE parent_folder_id=$1) OR EXISTS(SELECT 1 FROM user_log_placements WHERE folder_id=$1)`, folderID).Scan(&children); e != nil {
			return e
		}
		if children {
			return core.ErrFolderNotEmpty
		}
		if _, e = tx.Exec(ctx, `DELETE FROM folders WHERE id=$1`, folderID); e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, `UPDATE folders SET position=position-1 WHERE user_id=$1 AND parent_folder_id IS NOT DISTINCT FROM $2 AND position>$3`, userID, parent, pos); e != nil {
			return e
		}
		return nil
	})
}
