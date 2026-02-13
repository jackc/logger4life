package backend

import (
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
	"github.com/jackc/pgx/v5/pgxpool"
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
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Fields    []fieldDefinition `json:"fields"`
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt time.Time         `json:"updated_at"`
}

type createLogEntryRequest struct {
	Fields map[string]any `json:"fields"`
}

type logEntryResponse struct {
	ID        string         `json:"id"`
	LogID     string         `json:"log_id"`
	Fields    map[string]any `json:"fields"`
	CreatedAt time.Time      `json:"created_at"`
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

		if req.Fields == nil {
			req.Fields = []fieldDefinition{}
		}
		if err := validateFieldDefinitions(req.Fields); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}

		var l logResponse
		err := pool.QueryRow(r.Context(),
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
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			return
		}

		if l.Fields == nil {
			l.Fields = []fieldDefinition{}
		}
		writeJSON(w, http.StatusCreated, l)
	}
}

func handleListLogs(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := userFromContext(r.Context())

		rows, err := pool.Query(r.Context(),
			`SELECT id, name, fields, created_at, updated_at FROM logs
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
			if err := rows.Scan(&l.ID, &l.Name, &l.Fields, &l.CreatedAt, &l.UpdatedAt); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
				return
			}
			if l.Fields == nil {
				l.Fields = []fieldDefinition{}
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
			`SELECT id, name, fields, created_at, updated_at FROM logs
			 WHERE id = $1 AND user_id = $2`,
			logID, user.ID,
		).Scan(&l.ID, &l.Name, &l.Fields, &l.CreatedAt, &l.UpdatedAt)

		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "log not found"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			return
		}

		if l.Fields == nil {
			l.Fields = []fieldDefinition{}
		}
		writeJSON(w, http.StatusOK, l)
	}
}

func handleCreateLogEntry(pool *pgxpool.Pool) http.HandlerFunc {
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

		var ownerID string
		var logFields []fieldDefinition
		err := pool.QueryRow(r.Context(),
			`SELECT user_id, fields FROM logs WHERE id = $1`,
			logID,
		).Scan(&ownerID, &logFields)

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

		if err := validateFieldValues(logFields, req.Fields); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}

		var entry logEntryResponse
		err = pool.QueryRow(r.Context(),
			`INSERT INTO log_entries (log_id, fields) VALUES ($1, $2)
			 RETURNING id, log_id, fields, created_at`,
			logID, req.Fields,
		).Scan(&entry.ID, &entry.LogID, &entry.Fields, &entry.CreatedAt)

		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			return
		}

		if entry.Fields == nil {
			entry.Fields = map[string]any{}
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
			`SELECT id, log_id, fields, created_at FROM log_entries
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
			if err := rows.Scan(&e.ID, &e.LogID, &e.Fields, &e.CreatedAt); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
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
