package backend

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
)

type folderResponse struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	ParentFolderID *string   `json:"parent_folder_id"`
	Position       int       `json:"position"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type createFolderRequest struct {
	Name           string  `json:"name"`
	ParentFolderID *string `json:"parent_folder_id"`
}

type renameFolderRequest struct {
	Name string `json:"name"`
}

type moveFolderRequest struct {
	ParentFolderID *string `json:"parent_folder_id"`
	Position       int     `json:"position"`
}

// folderOwnedByUser returns nil if a folder with the given id is owned by the
// user. Returns pgx.ErrNoRows if the folder doesn't exist or belongs to
// someone else, so callers can map both cases to a 404.
func folderOwnedByUser(ctx context.Context, q rowQuerier, folderID, userID string) error {
	var owner string
	err := q.QueryRow(ctx, `SELECT user_id FROM folders WHERE id = $1`, folderID).Scan(&owner)
	if err != nil {
		return err
	}
	if owner != userID {
		return pgx.ErrNoRows
	}
	return nil
}

// rowQuerier abstracts over DB and Tx so helpers can run in either context.
type rowQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) Row
}

func handleCreateFolder(pool DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := userFromContext(r.Context())

		var req createFolderRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}

		req.Name = strings.TrimSpace(req.Name)
		if len(req.Name) == 0 || len(req.Name) > 100 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name must be 1-100 characters"})
			return
		}

		if req.ParentFolderID != nil {
			if err := folderOwnedByUser(r.Context(), pool, *req.ParentFolderID, user.ID); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					writeJSON(w, http.StatusBadRequest, map[string]string{"error": "parent folder not found"})
					return
				}
				internalError(w, r, err)
				return
			}
		}

		var f folderResponse
		err := pool.QueryRow(r.Context(),
			`INSERT INTO folders (user_id, parent_folder_id, name, position)
			 SELECT $1, $2, $3, COALESCE(max(position) + 1, 0)
			 FROM folders WHERE user_id = $1 AND parent_folder_id IS NOT DISTINCT FROM $2
			 RETURNING id, name, parent_folder_id, position, created_at, updated_at`,
			user.ID, req.ParentFolderID, req.Name,
		).Scan(&f.ID, &f.Name, &f.ParentFolderID, &f.Position, &f.CreatedAt, &f.UpdatedAt)
		if err != nil {
			internalError(w, r, err)
			return
		}
		writeJSON(w, http.StatusCreated, f)
	}
}

func handleListFolders(pool DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := userFromContext(r.Context())

		rows, err := pool.Query(r.Context(),
			`SELECT id, name, parent_folder_id, position, created_at, updated_at
			 FROM folders WHERE user_id = $1
			 ORDER BY parent_folder_id NULLS FIRST, position`,
			user.ID,
		)
		if err != nil {
			internalError(w, r, err)
			return
		}
		defer rows.Close()

		folders := []folderResponse{}
		for rows.Next() {
			var f folderResponse
			if err := rows.Scan(&f.ID, &f.Name, &f.ParentFolderID, &f.Position, &f.CreatedAt, &f.UpdatedAt); err != nil {
				internalError(w, r, err)
				return
			}
			folders = append(folders, f)
		}
		if err := rows.Err(); err != nil {
			internalError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, folders)
	}
}

func handleRenameFolder(pool DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := userFromContext(r.Context())
		folderID := chi.URLParam(r, "folderID")

		var req renameFolderRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}
		req.Name = strings.TrimSpace(req.Name)
		if len(req.Name) == 0 || len(req.Name) > 100 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name must be 1-100 characters"})
			return
		}

		var f folderResponse
		err := pool.QueryRow(r.Context(),
			`UPDATE folders SET name = $1, updated_at = now()
			 WHERE id = $2 AND user_id = $3
			 RETURNING id, name, parent_folder_id, position, created_at, updated_at`,
			req.Name, folderID, user.ID,
		).Scan(&f.ID, &f.Name, &f.ParentFolderID, &f.Position, &f.CreatedAt, &f.UpdatedAt)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "folder not found"})
				return
			}
			internalError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, f)
	}
}

func handleMoveFolder(pool DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := userFromContext(r.Context())
		folderID := chi.URLParam(r, "folderID")

		var req moveFolderRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}
		if req.Position < 0 {
			req.Position = 0
		}

		tx, err := pool.Begin(r.Context())
		if err != nil {
			internalError(w, r, err)
			return
		}
		defer tx.Rollback(r.Context())

		var oldParent *string
		var oldPosition int
		var owner string
		err = tx.QueryRow(r.Context(),
			`SELECT user_id, parent_folder_id, position FROM folders WHERE id = $1 FOR UPDATE`,
			folderID,
		).Scan(&owner, &oldParent, &oldPosition)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "folder not found"})
				return
			}
			internalError(w, r, err)
			return
		}
		if owner != user.ID {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "folder not found"})
			return
		}

		if req.ParentFolderID != nil {
			if *req.ParentFolderID == folderID {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "folder cannot be its own parent"})
				return
			}
			if err := folderOwnedByUser(r.Context(), tx, *req.ParentFolderID, user.ID); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					writeJSON(w, http.StatusBadRequest, map[string]string{"error": "parent folder not found"})
					return
				}
				internalError(w, r, err)
				return
			}
			// Cycle prevention: new parent must not be a descendant of folderID.
			var cycle bool
			err = tx.QueryRow(r.Context(),
				`WITH RECURSIVE descendants AS (
					SELECT id FROM folders WHERE id = $1
					UNION ALL
					SELECT f.id FROM folders f JOIN descendants d ON f.parent_folder_id = d.id
				)
				SELECT EXISTS(SELECT 1 FROM descendants WHERE id = $2)`,
				folderID, *req.ParentFolderID,
			).Scan(&cycle)
			if err != nil {
				internalError(w, r, err)
				return
			}
			if cycle {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "cannot move a folder into its own descendant"})
				return
			}
		}

		sameParent := (oldParent == nil && req.ParentFolderID == nil) ||
			(oldParent != nil && req.ParentFolderID != nil && *oldParent == *req.ParentFolderID)

		if sameParent {
			// Renumber within a single sibling group.
			if req.Position > oldPosition {
				// Cap at last-position-among-siblings (count - 1).
				var siblingCount int
				if err := tx.QueryRow(r.Context(),
					`SELECT count(*) FROM folders WHERE user_id = $1 AND parent_folder_id IS NOT DISTINCT FROM $2`,
					user.ID, oldParent,
				).Scan(&siblingCount); err != nil {
					internalError(w, r, err)
					return
				}
				if req.Position > siblingCount-1 {
					req.Position = siblingCount - 1
				}
				if _, err := tx.Exec(r.Context(),
					`UPDATE folders SET position = position - 1
					 WHERE user_id = $1 AND parent_folder_id IS NOT DISTINCT FROM $2
					   AND position > $3 AND position <= $4`,
					user.ID, oldParent, oldPosition, req.Position,
				); err != nil {
					internalError(w, r, err)
					return
				}
			} else if req.Position < oldPosition {
				if _, err := tx.Exec(r.Context(),
					`UPDATE folders SET position = position + 1
					 WHERE user_id = $1 AND parent_folder_id IS NOT DISTINCT FROM $2
					   AND position >= $3 AND position < $4`,
					user.ID, oldParent, req.Position, oldPosition,
				); err != nil {
					internalError(w, r, err)
					return
				}
			}
			if _, err := tx.Exec(r.Context(),
				`UPDATE folders SET position = $1, updated_at = now() WHERE id = $2`,
				req.Position, folderID,
			); err != nil {
				internalError(w, r, err)
				return
			}
		} else {
			// Different parent: close gap at source, open gap at destination.
			var destCount int
			if err := tx.QueryRow(r.Context(),
				`SELECT count(*) FROM folders WHERE user_id = $1 AND parent_folder_id IS NOT DISTINCT FROM $2`,
				user.ID, req.ParentFolderID,
			).Scan(&destCount); err != nil {
				internalError(w, r, err)
				return
			}
			if req.Position > destCount {
				req.Position = destCount
			}

			if _, err := tx.Exec(r.Context(),
				`UPDATE folders SET position = position - 1
				 WHERE user_id = $1 AND parent_folder_id IS NOT DISTINCT FROM $2 AND position > $3`,
				user.ID, oldParent, oldPosition,
			); err != nil {
				internalError(w, r, err)
				return
			}
			if _, err := tx.Exec(r.Context(),
				`UPDATE folders SET position = position + 1
				 WHERE user_id = $1 AND parent_folder_id IS NOT DISTINCT FROM $2 AND position >= $3`,
				user.ID, req.ParentFolderID, req.Position,
			); err != nil {
				internalError(w, r, err)
				return
			}
			if _, err := tx.Exec(r.Context(),
				`UPDATE folders SET parent_folder_id = $1, position = $2, updated_at = now() WHERE id = $3`,
				req.ParentFolderID, req.Position, folderID,
			); err != nil {
				internalError(w, r, err)
				return
			}
		}

		if err := tx.Commit(r.Context()); err != nil {
			internalError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func handleDeleteFolder(pool DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := userFromContext(r.Context())
		folderID := chi.URLParam(r, "folderID")

		tx, err := pool.Begin(r.Context())
		if err != nil {
			internalError(w, r, err)
			return
		}
		defer tx.Rollback(r.Context())

		var owner string
		var parent *string
		var position int
		err = tx.QueryRow(r.Context(),
			`SELECT user_id, parent_folder_id, position FROM folders WHERE id = $1 FOR UPDATE`,
			folderID,
		).Scan(&owner, &parent, &position)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "folder not found"})
				return
			}
			internalError(w, r, err)
			return
		}
		if owner != user.ID {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "folder not found"})
			return
		}

		var hasChildren bool
		err = tx.QueryRow(r.Context(),
			`SELECT EXISTS(SELECT 1 FROM folders WHERE parent_folder_id = $1)
			    OR EXISTS(SELECT 1 FROM user_log_placements WHERE folder_id = $1)`,
			folderID,
		).Scan(&hasChildren)
		if err != nil {
			internalError(w, r, err)
			return
		}
		if hasChildren {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "folder is not empty"})
			return
		}

		if _, err := tx.Exec(r.Context(), `DELETE FROM folders WHERE id = $1`, folderID); err != nil {
			internalError(w, r, err)
			return
		}
		if _, err := tx.Exec(r.Context(),
			`UPDATE folders SET position = position - 1
			 WHERE user_id = $1 AND parent_folder_id IS NOT DISTINCT FROM $2 AND position > $3`,
			user.ID, parent, position,
		); err != nil {
			internalError(w, r, err)
			return
		}

		if err := tx.Commit(r.Context()); err != nil {
			internalError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
