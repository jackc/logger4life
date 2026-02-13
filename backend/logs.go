package backend

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type createLogRequest struct {
	Name string `json:"name"`
}

type logResponse struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type logEntryResponse struct {
	ID        string    `json:"id"`
	LogID     string    `json:"log_id"`
	CreatedAt time.Time `json:"created_at"`
}

func handleCreateLog(pool *pgxpool.Pool) http.HandlerFunc {
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

		var l logResponse
		err := pool.QueryRow(r.Context(),
			`INSERT INTO logs (user_id, name) VALUES ($1, $2)
			 RETURNING id, name, created_at, updated_at`,
			user.ID, req.Name,
		).Scan(&l.ID, &l.Name, &l.CreatedAt, &l.UpdatedAt)

		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				writeJSON(w, http.StatusConflict, map[string]string{"error": "a log with that name already exists"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			return
		}

		writeJSON(w, http.StatusCreated, l)
	}
}

func handleListLogs(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := userFromContext(r.Context())

		rows, err := pool.Query(r.Context(),
			`SELECT id, name, created_at, updated_at FROM logs
			 WHERE user_id = $1 ORDER BY lower(name)`,
			user.ID,
		)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			return
		}
		defer rows.Close()

		logs := []logResponse{}
		for rows.Next() {
			var l logResponse
			if err := rows.Scan(&l.ID, &l.Name, &l.CreatedAt, &l.UpdatedAt); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
				return
			}
			logs = append(logs, l)
		}

		writeJSON(w, http.StatusOK, logs)
	}
}

func handleGetLog(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := userFromContext(r.Context())
		logID := chi.URLParam(r, "logID")

		var l logResponse
		err := pool.QueryRow(r.Context(),
			`SELECT id, name, created_at, updated_at FROM logs
			 WHERE id = $1 AND user_id = $2`,
			logID, user.ID,
		).Scan(&l.ID, &l.Name, &l.CreatedAt, &l.UpdatedAt)

		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "log not found"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			return
		}

		writeJSON(w, http.StatusOK, l)
	}
}

func handleCreateLogEntry(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := userFromContext(r.Context())
		logID := chi.URLParam(r, "logID")

		var ownerID string
		err := pool.QueryRow(r.Context(),
			`SELECT user_id FROM logs WHERE id = $1`,
			logID,
		).Scan(&ownerID)

		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "log not found"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			return
		}
		if ownerID != user.ID {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "log not found"})
			return
		}

		var entry logEntryResponse
		err = pool.QueryRow(r.Context(),
			`INSERT INTO log_entries (log_id) VALUES ($1)
			 RETURNING id, log_id, created_at`,
			logID,
		).Scan(&entry.ID, &entry.LogID, &entry.CreatedAt)

		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			return
		}

		writeJSON(w, http.StatusCreated, entry)
	}
}

func handleListLogEntries(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := userFromContext(r.Context())
		logID := chi.URLParam(r, "logID")

		var ownerID string
		err := pool.QueryRow(r.Context(),
			`SELECT user_id FROM logs WHERE id = $1`,
			logID,
		).Scan(&ownerID)

		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "log not found"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			return
		}
		if ownerID != user.ID {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "log not found"})
			return
		}

		rows, err := pool.Query(r.Context(),
			`SELECT id, log_id, created_at FROM log_entries
			 WHERE log_id = $1 ORDER BY created_at DESC`,
			logID,
		)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			return
		}
		defer rows.Close()

		entries := []logEntryResponse{}
		for rows.Next() {
			var e logEntryResponse
			if err := rows.Scan(&e.ID, &e.LogID, &e.CreatedAt); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
				return
			}
			entries = append(entries, e)
		}

		writeJSON(w, http.StatusOK, entries)
	}
}
