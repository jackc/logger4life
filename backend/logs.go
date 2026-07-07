package backend

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type fieldDefinition struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Required bool   `json:"required"`
}

type createLogRequest struct {
	Name   string            `json:"name"`
	Fields []fieldDefinition `json:"fields"`
}

type logResponse struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	Fields       []fieldDefinition `json:"fields"`
	IsOwner      bool              `json:"is_owner"`
	ShareToken   *string           `json:"share_token,omitempty"`
	FolderID     *string           `json:"folder_id"`
	Position     int               `json:"position"`
	PinnedToHome bool              `json:"pinned_to_home"`
	HomePosition int               `json:"home_position"`
	CreatedAt    time.Time         `json:"created_at"`
	UpdatedAt    time.Time         `json:"updated_at"`
}

type updateLogPlacementRequest struct {
	FolderID *string `json:"folder_id"`
	Position int     `json:"position"`
}

type pinLogRequest struct {
	Pinned bool `json:"pinned"`
}

type updateHomePositionRequest struct {
	HomePosition int `json:"home_position"`
}

type createLogEntryRequest struct {
	Fields map[string]any `json:"fields"`
}

type updateLogEntryRequest struct {
	Fields     map[string]any `json:"fields"`
	OccurredAt time.Time      `json:"occurred_at"`
}

type logEntryResponse struct {
	ID         string         `json:"id"`
	LogID      string         `json:"log_id"`
	UserID     string         `json:"user_id"`
	Username   string         `json:"username"`
	Fields     map[string]any `json:"fields"`
	OccurredAt time.Time      `json:"occurred_at"`
	CreatedAt  time.Time      `json:"created_at"`
	UpdatedAt  time.Time      `json:"updated_at"`
}

func validateFieldDefinitions(fields []fieldDefinition) error {
	if len(fields) > 20 {
		return fmt.Errorf("too many fields (max 20)")
	}
	seen := make(map[string]bool)
	for i, f := range fields {
		f.Name = strings.TrimSpace(f.Name)
		fields[i].Name = f.Name
		if f.Name == "" {
			return fmt.Errorf("field name must not be empty")
		}
		if len(f.Name) > 100 {
			return fmt.Errorf("field name must be 1-100 characters")
		}
		lower := strings.ToLower(f.Name)
		if seen[lower] {
			return fmt.Errorf("duplicate field name: %s", f.Name)
		}
		seen[lower] = true
		if f.Type != "text" && f.Type != "number" && f.Type != "boolean" {
			return fmt.Errorf("field type must be 'text', 'number', or 'boolean'")
		}
	}
	return nil
}

func validateFieldValues(definitions []fieldDefinition, values map[string]any) error {
	if values == nil {
		values = make(map[string]any)
	}

	defMap := make(map[string]fieldDefinition)
	for _, d := range definitions {
		defMap[d.Name] = d
	}

	for name := range values {
		if _, ok := defMap[name]; !ok {
			return fmt.Errorf("unknown field: %s", name)
		}
	}

	for _, def := range definitions {
		v, exists := values[def.Name]
		if !exists || v == nil {
			if def.Required {
				return fmt.Errorf("field %q is required", def.Name)
			}
			continue
		}
		switch def.Type {
		case "number":
			s, ok := v.(string)
			if !ok {
				return fmt.Errorf("field %q must be a numeric string", def.Name)
			}
			if def.Required && strings.TrimSpace(s) == "" {
				return fmt.Errorf("field %q is required", def.Name)
			}
			if strings.TrimSpace(s) != "" {
				if _, err := strconv.ParseFloat(s, 64); err != nil {
					return fmt.Errorf("field %q must be a valid number", def.Name)
				}
			}
		case "text":
			s, ok := v.(string)
			if !ok {
				return fmt.Errorf("field %q must be a string", def.Name)
			}
			if def.Required && strings.TrimSpace(s) == "" {
				return fmt.Errorf("field %q is required", def.Name)
			}
		case "boolean":
			if _, ok := v.(bool); !ok {
				return fmt.Errorf("field %q must be true or false", def.Name)
			}
		}
	}
	return nil
}

func handleCreateLog(pool DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := userFromContext(r.Context())

		var req createLogRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}

		req.Name = strings.TrimSpace(req.Name)
		if len(req.Name) == 0 || len(req.Name) > 100 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name must be 1-100 characters"})
			return
		}

		if req.Fields == nil {
			req.Fields = []fieldDefinition{}
		}
		if err := validateFieldDefinitions(req.Fields); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}

		tx, err := pool.Begin(r.Context())
		if err != nil {
			internalError(w, r, err)
			return
		}
		defer tx.Rollback(r.Context())

		var l logResponse
		err = tx.QueryRow(r.Context(),
			`INSERT INTO logs (user_id, name, fields) VALUES ($1, $2, $3)
			 RETURNING id, name, fields, created_at, updated_at`,
			user.ID, req.Name, req.Fields,
		).Scan(&l.ID, &l.Name, &l.Fields, &l.CreatedAt, &l.UpdatedAt)

		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				writeJSON(w, http.StatusConflict, map[string]string{"error": "a log with that name already exists"})
				return
			}
			internalError(w, r, err)
			return
		}

		err = tx.QueryRow(r.Context(),
			`INSERT INTO user_log_placements (user_id, log_id, folder_id, position, pinned_to_home, home_position)
			 SELECT $1, $2, NULL,
			        COALESCE(max(position) FILTER (WHERE folder_id IS NULL) + 1, 0),
			        true,
			        COALESCE(max(home_position) FILTER (WHERE pinned_to_home) + 1, 0)
			 FROM user_log_placements WHERE user_id = $1
			 RETURNING position, pinned_to_home, home_position`,
			user.ID, l.ID,
		).Scan(&l.Position, &l.PinnedToHome, &l.HomePosition)
		if err != nil {
			internalError(w, r, err)
			return
		}

		if err := tx.Commit(r.Context()); err != nil {
			internalError(w, r, err)
			return
		}

		l.IsOwner = true
		if l.Fields == nil {
			l.Fields = []fieldDefinition{}
		}
		writeJSON(w, http.StatusCreated, l)
	}
}

// listLogsForUser returns all logs the user owns or has been shared on,
// ordered by their per-user placement (folder then position). Shared by
// handleListLogs (HTTP) and the MCP list_logs tool.
func listLogsForUser(ctx context.Context, pool DB, userID string) ([]logResponse, error) {
	rows, err := pool.Query(ctx,
		`SELECT l.id, l.name, l.fields, l.created_at, l.updated_at,
		        (l.user_id = $1) AS is_owner,
		        p.folder_id, p.position, p.pinned_to_home, p.home_position
		 FROM logs l
		 JOIN user_log_placements p ON p.log_id = l.id AND p.user_id = $1
		 WHERE l.user_id = $1
		    OR EXISTS (SELECT 1 FROM log_shares ls WHERE ls.log_id = l.id AND ls.user_id = $1)
		 ORDER BY p.folder_id NULLS FIRST, p.position`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	logs := []logResponse{}
	for rows.Next() {
		var l logResponse
		if err := rows.Scan(&l.ID, &l.Name, &l.Fields, &l.CreatedAt, &l.UpdatedAt, &l.IsOwner, &l.FolderID, &l.Position, &l.PinnedToHome, &l.HomePosition); err != nil {
			return nil, err
		}
		if l.Fields == nil {
			l.Fields = []fieldDefinition{}
		}
		logs = append(logs, l)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return logs, nil
}

func handleListLogs(pool DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := userFromContext(r.Context())
		logs, err := listLogsForUser(r.Context(), pool, user.ID)
		if err != nil {
			internalError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, logs)
	}
}

func handleGetLog(pool DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := userFromContext(r.Context())
		logID := chi.URLParam(r, "logID")

		access, err := checkLogAccess(r.Context(), pool, logID, user.ID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "log not found"})
				return
			}
			internalError(w, r, err)
			return
		}

		var l logResponse
		var shareToken []byte
		err = pool.QueryRow(r.Context(),
			`SELECT l.id, l.name, l.fields, l.share_token, l.created_at, l.updated_at,
			        p.folder_id, p.position, p.pinned_to_home, p.home_position
			 FROM logs l
			 JOIN user_log_placements p ON p.log_id = l.id AND p.user_id = $1
			 WHERE l.id = $2`,
			user.ID, logID,
		).Scan(&l.ID, &l.Name, &l.Fields, &shareToken, &l.CreatedAt, &l.UpdatedAt, &l.FolderID, &l.Position, &l.PinnedToHome, &l.HomePosition)
		if err != nil {
			internalError(w, r, err)
			return
		}

		l.IsOwner = access.IsOwner
		if access.IsOwner && shareToken != nil {
			tokenHex := hex.EncodeToString(shareToken)
			l.ShareToken = &tokenHex
		}

		if l.Fields == nil {
			l.Fields = []fieldDefinition{}
		}
		writeJSON(w, http.StatusOK, l)
	}
}

func handleUpdateLog(pool DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := userFromContext(r.Context())
		logID := chi.URLParam(r, "logID")

		var req createLogRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}

		req.Name = strings.TrimSpace(req.Name)
		if len(req.Name) == 0 || len(req.Name) > 100 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name must be 1-100 characters"})
			return
		}

		if req.Fields == nil {
			req.Fields = []fieldDefinition{}
		}
		if err := validateFieldDefinitions(req.Fields); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}

		var l logResponse
		var shareToken []byte
		err := pool.QueryRow(r.Context(),
			`WITH updated AS (
				UPDATE logs SET name = $1, fields = $2, updated_at = now()
				WHERE id = $3 AND user_id = $4
				RETURNING id, name, fields, share_token, created_at, updated_at
			)
			SELECT u.id, u.name, u.fields, u.share_token, u.created_at, u.updated_at,
			       p.folder_id, p.position, p.pinned_to_home, p.home_position
			FROM updated u
			JOIN user_log_placements p ON p.log_id = u.id AND p.user_id = $4`,
			req.Name, req.Fields, logID, user.ID,
		).Scan(&l.ID, &l.Name, &l.Fields, &shareToken, &l.CreatedAt, &l.UpdatedAt, &l.FolderID, &l.Position, &l.PinnedToHome, &l.HomePosition)

		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "log not found"})
				return
			}
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				writeJSON(w, http.StatusConflict, map[string]string{"error": "a log with that name already exists"})
				return
			}
			internalError(w, r, err)
			return
		}

		l.IsOwner = true
		if shareToken != nil {
			tokenHex := hex.EncodeToString(shareToken)
			l.ShareToken = &tokenHex
		}
		if l.Fields == nil {
			l.Fields = []fieldDefinition{}
		}
		writeJSON(w, http.StatusOK, l)
	}
}

func handleDeleteLog(pool DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := userFromContext(r.Context())
		logID := chi.URLParam(r, "logID")

		tag, err := pool.Exec(r.Context(),
			`DELETE FROM logs WHERE id = $1 AND user_id = $2`,
			logID, user.ID,
		)

		if err != nil {
			internalError(w, r, err)
			return
		}
		if tag.RowsAffected() == 0 {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "log not found"})
			return
		}

		w.WriteHeader(http.StatusNoContent)
	}
}

func handleCreateLogEntry(pool DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := userFromContext(r.Context())
		logID := chi.URLParam(r, "logID")

		var req createLogEntryRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}
		if req.Fields == nil {
			req.Fields = map[string]any{}
		}

		access, err := checkLogAccess(r.Context(), pool, logID, user.ID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "log not found"})
				return
			}
			internalError(w, r, err)
			return
		}

		if err := validateFieldValues(access.Fields, req.Fields); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}

		var entry logEntryResponse
		err = pool.QueryRow(r.Context(),
			`INSERT INTO log_entries (log_id, user_id, fields) VALUES ($1, $2, $3)
			 RETURNING id, log_id, user_id, fields, occurred_at, created_at, updated_at`,
			logID, user.ID, req.Fields,
		).Scan(&entry.ID, &entry.LogID, &entry.UserID, &entry.Fields, &entry.OccurredAt, &entry.CreatedAt, &entry.UpdatedAt)

		if err != nil {
			internalError(w, r, err)
			return
		}

		entry.Username = user.Username
		if entry.Fields == nil {
			entry.Fields = map[string]any{}
		}
		writeJSON(w, http.StatusCreated, entry)
	}
}

func handleUpdateLogEntry(pool DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := userFromContext(r.Context())
		logID := chi.URLParam(r, "logID")
		entryID := chi.URLParam(r, "entryID")

		var req updateLogEntryRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}
		if req.Fields == nil {
			req.Fields = map[string]any{}
		}
		if req.OccurredAt.IsZero() {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "occurred_at is required"})
			return
		}

		access, err := checkLogAccess(r.Context(), pool, logID, user.ID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "log not found"})
				return
			}
			internalError(w, r, err)
			return
		}

		if err := validateFieldValues(access.Fields, req.Fields); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}

		var entry logEntryResponse
		err = pool.QueryRow(r.Context(),
			`UPDATE log_entries SET fields = $1, occurred_at = $2, updated_at = now()
			 WHERE id = $3 AND log_id = $4
			 RETURNING id, log_id, user_id, fields, occurred_at, created_at, updated_at`,
			req.Fields, req.OccurredAt, entryID, logID,
		).Scan(&entry.ID, &entry.LogID, &entry.UserID, &entry.Fields, &entry.OccurredAt, &entry.CreatedAt, &entry.UpdatedAt)

		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "entry not found"})
				return
			}
			internalError(w, r, err)
			return
		}

		err = pool.QueryRow(r.Context(),
			`SELECT username FROM users WHERE id = $1`, entry.UserID,
		).Scan(&entry.Username)
		if err != nil {
			internalError(w, r, err)
			return
		}

		if entry.Fields == nil {
			entry.Fields = map[string]any{}
		}
		writeJSON(w, http.StatusOK, entry)
	}
}

func handleDeleteLogEntry(pool DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := userFromContext(r.Context())
		logID := chi.URLParam(r, "logID")
		entryID := chi.URLParam(r, "entryID")

		_, err := checkLogAccess(r.Context(), pool, logID, user.ID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "log not found"})
				return
			}
			internalError(w, r, err)
			return
		}

		tag, err := pool.Exec(r.Context(),
			`DELETE FROM log_entries WHERE id = $1 AND log_id = $2`,
			entryID, logID,
		)

		if err != nil {
			internalError(w, r, err)
			return
		}
		if tag.RowsAffected() == 0 {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "entry not found"})
			return
		}

		w.WriteHeader(http.StatusNoContent)
	}
}

func handleListLogEntries(pool DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := userFromContext(r.Context())
		logID := chi.URLParam(r, "logID")

		_, err := checkLogAccess(r.Context(), pool, logID, user.ID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "log not found"})
				return
			}
			internalError(w, r, err)
			return
		}

		rows, err := pool.Query(r.Context(),
			`SELECT le.id, le.log_id, le.user_id, u.username, le.fields, le.occurred_at, le.created_at, le.updated_at
			 FROM log_entries le
			 JOIN users u ON le.user_id = u.id
			 WHERE le.log_id = $1
			 ORDER BY le.occurred_at DESC`,
			logID,
		)
		if err != nil {
			internalError(w, r, err)
			return
		}
		defer rows.Close()

		entries := []logEntryResponse{}
		for rows.Next() {
			var e logEntryResponse
			if err := rows.Scan(&e.ID, &e.LogID, &e.UserID, &e.Username, &e.Fields, &e.OccurredAt, &e.CreatedAt, &e.UpdatedAt); err != nil {
				internalError(w, r, err)
				return
			}
			if e.Fields == nil {
				e.Fields = map[string]any{}
			}
			entries = append(entries, e)
		}

		writeJSON(w, http.StatusOK, entries)
	}
}

// handleUpdateLogPlacement moves the caller's placement row for a log: changes
// which folder it sits in and/or its position among siblings. The placement
// is per-user — moving here does not affect any other user who can see the
// same shared log.
func handleUpdateLogPlacement(pool DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := userFromContext(r.Context())
		logID := chi.URLParam(r, "logID")

		var req updateLogPlacementRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}
		if req.Position < 0 {
			req.Position = 0
		}

		if _, err := checkLogAccess(r.Context(), pool, logID, user.ID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "log not found"})
				return
			}
			internalError(w, r, err)
			return
		}

		tx, err := pool.Begin(r.Context())
		if err != nil {
			internalError(w, r, err)
			return
		}
		defer tx.Rollback(r.Context())

		var oldFolder *string
		var oldPosition int
		err = tx.QueryRow(r.Context(),
			`SELECT folder_id, position FROM user_log_placements
			 WHERE user_id = $1 AND log_id = $2 FOR UPDATE`,
			user.ID, logID,
		).Scan(&oldFolder, &oldPosition)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "log not found"})
				return
			}
			internalError(w, r, err)
			return
		}

		if req.FolderID != nil {
			if err := folderOwnedByUser(r.Context(), tx, *req.FolderID, user.ID); err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					writeJSON(w, http.StatusBadRequest, map[string]string{"error": "folder not found"})
					return
				}
				internalError(w, r, err)
				return
			}
		}

		sameFolder := (oldFolder == nil && req.FolderID == nil) ||
			(oldFolder != nil && req.FolderID != nil && *oldFolder == *req.FolderID)

		if sameFolder {
			if req.Position > oldPosition {
				var siblingCount int
				if err := tx.QueryRow(r.Context(),
					`SELECT count(*) FROM user_log_placements WHERE user_id = $1 AND folder_id IS NOT DISTINCT FROM $2`,
					user.ID, oldFolder,
				).Scan(&siblingCount); err != nil {
					internalError(w, r, err)
					return
				}
				if req.Position > siblingCount-1 {
					req.Position = siblingCount - 1
				}
				if _, err := tx.Exec(r.Context(),
					`UPDATE user_log_placements SET position = position - 1
					 WHERE user_id = $1 AND folder_id IS NOT DISTINCT FROM $2
					   AND position > $3 AND position <= $4`,
					user.ID, oldFolder, oldPosition, req.Position,
				); err != nil {
					internalError(w, r, err)
					return
				}
			} else if req.Position < oldPosition {
				if _, err := tx.Exec(r.Context(),
					`UPDATE user_log_placements SET position = position + 1
					 WHERE user_id = $1 AND folder_id IS NOT DISTINCT FROM $2
					   AND position >= $3 AND position < $4`,
					user.ID, oldFolder, req.Position, oldPosition,
				); err != nil {
					internalError(w, r, err)
					return
				}
			}
			if _, err := tx.Exec(r.Context(),
				`UPDATE user_log_placements SET position = $1, updated_at = now()
				 WHERE user_id = $2 AND log_id = $3`,
				req.Position, user.ID, logID,
			); err != nil {
				internalError(w, r, err)
				return
			}
		} else {
			var destCount int
			if err := tx.QueryRow(r.Context(),
				`SELECT count(*) FROM user_log_placements WHERE user_id = $1 AND folder_id IS NOT DISTINCT FROM $2`,
				user.ID, req.FolderID,
			).Scan(&destCount); err != nil {
				internalError(w, r, err)
				return
			}
			if req.Position > destCount {
				req.Position = destCount
			}

			if _, err := tx.Exec(r.Context(),
				`UPDATE user_log_placements SET position = position - 1
				 WHERE user_id = $1 AND folder_id IS NOT DISTINCT FROM $2 AND position > $3`,
				user.ID, oldFolder, oldPosition,
			); err != nil {
				internalError(w, r, err)
				return
			}
			if _, err := tx.Exec(r.Context(),
				`UPDATE user_log_placements SET position = position + 1
				 WHERE user_id = $1 AND folder_id IS NOT DISTINCT FROM $2 AND position >= $3`,
				user.ID, req.FolderID, req.Position,
			); err != nil {
				internalError(w, r, err)
				return
			}
			if _, err := tx.Exec(r.Context(),
				`UPDATE user_log_placements SET folder_id = $1, position = $2, updated_at = now()
				 WHERE user_id = $3 AND log_id = $4`,
				req.FolderID, req.Position, user.ID, logID,
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

// handlePinLog toggles whether the log appears on the caller's quick-log
// home page. Pinning a previously-unpinned log appends it to the end of the
// home list. Unpinning leaves home_position untouched (no renumbering),
// since the home page filters by pinned_to_home and gaps don't render.
func handlePinLog(pool DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := userFromContext(r.Context())
		logID := chi.URLParam(r, "logID")

		var req pinLogRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}

		if _, err := checkLogAccess(r.Context(), pool, logID, user.ID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "log not found"})
				return
			}
			internalError(w, r, err)
			return
		}

		tx, err := pool.Begin(r.Context())
		if err != nil {
			internalError(w, r, err)
			return
		}
		defer tx.Rollback(r.Context())

		var currentlyPinned bool
		err = tx.QueryRow(r.Context(),
			`SELECT pinned_to_home FROM user_log_placements
			 WHERE user_id = $1 AND log_id = $2 FOR UPDATE`,
			user.ID, logID,
		).Scan(&currentlyPinned)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "log not found"})
				return
			}
			internalError(w, r, err)
			return
		}

		if req.Pinned == currentlyPinned {
			if err := tx.Commit(r.Context()); err != nil {
				internalError(w, r, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}

		if req.Pinned {
			// Pinning: append to end of pinned siblings.
			if _, err := tx.Exec(r.Context(),
				`UPDATE user_log_placements
				 SET pinned_to_home = true,
				     home_position = COALESCE(
				         (SELECT max(home_position) + 1
				          FROM user_log_placements
				          WHERE user_id = $1 AND pinned_to_home),
				         0
				     ),
				     updated_at = now()
				 WHERE user_id = $1 AND log_id = $2`,
				user.ID, logID,
			); err != nil {
				internalError(w, r, err)
				return
			}
		} else {
			if _, err := tx.Exec(r.Context(),
				`UPDATE user_log_placements
				 SET pinned_to_home = false, updated_at = now()
				 WHERE user_id = $1 AND log_id = $2`,
				user.ID, logID,
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

// handleUpdateLogHomePosition reorders a pinned log within the caller's home
// page list. Only pinned siblings participate in renumbering; unpinned rows
// retain whatever home_position they had.
func handleUpdateLogHomePosition(pool DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := userFromContext(r.Context())
		logID := chi.URLParam(r, "logID")

		var req updateHomePositionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}
		if req.HomePosition < 0 {
			req.HomePosition = 0
		}

		if _, err := checkLogAccess(r.Context(), pool, logID, user.ID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "log not found"})
				return
			}
			internalError(w, r, err)
			return
		}

		tx, err := pool.Begin(r.Context())
		if err != nil {
			internalError(w, r, err)
			return
		}
		defer tx.Rollback(r.Context())

		var pinned bool
		var oldPosition int
		err = tx.QueryRow(r.Context(),
			`SELECT pinned_to_home, home_position FROM user_log_placements
			 WHERE user_id = $1 AND log_id = $2 FOR UPDATE`,
			user.ID, logID,
		).Scan(&pinned, &oldPosition)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "log not found"})
				return
			}
			internalError(w, r, err)
			return
		}
		if !pinned {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "log is not pinned to home"})
			return
		}

		// Cap target to last position among pinned siblings.
		var pinnedCount int
		if err := tx.QueryRow(r.Context(),
			`SELECT count(*) FROM user_log_placements WHERE user_id = $1 AND pinned_to_home`,
			user.ID,
		).Scan(&pinnedCount); err != nil {
			internalError(w, r, err)
			return
		}
		if req.HomePosition > pinnedCount-1 {
			req.HomePosition = pinnedCount - 1
		}
		if req.HomePosition == oldPosition {
			if err := tx.Commit(r.Context()); err != nil {
				internalError(w, r, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}

		if req.HomePosition > oldPosition {
			if _, err := tx.Exec(r.Context(),
				`UPDATE user_log_placements SET home_position = home_position - 1
				 WHERE user_id = $1 AND pinned_to_home
				   AND home_position > $2 AND home_position <= $3`,
				user.ID, oldPosition, req.HomePosition,
			); err != nil {
				internalError(w, r, err)
				return
			}
		} else {
			if _, err := tx.Exec(r.Context(),
				`UPDATE user_log_placements SET home_position = home_position + 1
				 WHERE user_id = $1 AND pinned_to_home
				   AND home_position >= $2 AND home_position < $3`,
				user.ID, req.HomePosition, oldPosition,
			); err != nil {
				internalError(w, r, err)
				return
			}
		}
		if _, err := tx.Exec(r.Context(),
			`UPDATE user_log_placements SET home_position = $1, updated_at = now()
			 WHERE user_id = $2 AND log_id = $3`,
			req.HomePosition, user.ID, logID,
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
