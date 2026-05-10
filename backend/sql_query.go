package backend

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	pgsqlarbiter "github.com/jackc/pgsqlarbiter-go"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	sqlQueryStatementTimeout = "5s"
	sqlQueryIdleTimeout      = "10s"
	sqlQueryMaxRows          = 1000
	sqlQueryMaxLength        = 10000
	savedQueryNameMaxLen     = 100
)

func newSQLArbiter() *pgsqlarbiter.Arbiter {
	return &pgsqlarbiter.Arbiter{
		AllowedStatementTypes: []pgsqlarbiter.StatementType{pgsqlarbiter.StatementSelect},
		AllowedTables:         []string{"logs", "log_entries"},
	}
}

type sqlExecuteRequest struct {
	Query string `json:"query"`
}

type sqlColumn struct {
	Name     string `json:"name"`
	DataType string `json:"data_type"`
}

type sqlExecuteResponse struct {
	Columns   []sqlColumn  `json:"columns"`
	Rows      [][]*string  `json:"rows"`
	RowCount  int          `json:"row_count"`
	Truncated bool         `json:"truncated"`
	ElapsedMs int64        `json:"elapsed_ms"`
}

func handleExecuteSQL(pool *pgxpool.Pool, arbiter *pgsqlarbiter.Arbiter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := userFromContext(r.Context())

		var req sqlExecuteRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}
		req.Query = strings.TrimSpace(req.Query)
		if req.Query == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "query is required"})
			return
		}
		if len(req.Query) > sqlQueryMaxLength {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "query is too long"})
			return
		}

		verdict, err := arbiter.Judge(req.Query)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if !verdict.Allowed {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": describeVerdict(verdict)})
			return
		}

		conn, err := pool.Acquire(r.Context())
		if err != nil {
			internalError(w, r, err)
			return
		}
		defer conn.Release()

		tx, err := conn.BeginTx(r.Context(), pgx.TxOptions{
			IsoLevel:   pgx.ReadCommitted,
			AccessMode: pgx.ReadOnly,
		})
		if err != nil {
			internalError(w, r, err)
			return
		}
		defer tx.Rollback(r.Context())

		// Configure session as the privileged role, then drop into the restricted
		// role. set_config(..., true) is transaction-local, equivalent to SET LOCAL.
		setup := []string{
			"SET LOCAL statement_timeout = '" + sqlQueryStatementTimeout + "'",
			"SET LOCAL idle_in_transaction_session_timeout = '" + sqlQueryIdleTimeout + "'",
		}
		for _, stmt := range setup {
			if _, err := tx.Exec(r.Context(), stmt); err != nil {
				internalError(w, r, err)
				return
			}
		}
		if _, err := tx.Exec(r.Context(), "SELECT set_config('app.current_user_id', $1, true)", user.ID); err != nil {
			internalError(w, r, err)
			return
		}
		if _, err := tx.Exec(r.Context(), "SET LOCAL ROLE logger4life_sql_user"); err != nil {
			internalError(w, r, err)
			return
		}
		if _, err := tx.Exec(r.Context(), "SET LOCAL search_path TO sql_query"); err != nil {
			internalError(w, r, err)
			return
		}

		start := time.Now()

		// pgx.QueryExecModeExec uses the extended protocol (which forbids
		// multi-statement queries) and skips the prepared-statement cache.
		// A single-element QueryResultFormats applies that format to every column.
		rows, err := tx.Query(r.Context(), req.Query,
			pgx.QueryExecModeExec,
			pgx.QueryResultFormats{pgx.TextFormatCode},
		)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": describePgError(err)})
			return
		}
		defer rows.Close()

		fields := rows.FieldDescriptions()
		typeMap := conn.Conn().TypeMap()
		columns := make([]sqlColumn, len(fields))
		for i, f := range fields {
			columns[i] = sqlColumn{
				Name:     string(f.Name),
				DataType: typeNameForOID(typeMap, f.DataTypeOID),
			}
		}

		resultRows := make([][]*string, 0)
		truncated := false
		for rows.Next() {
			if len(resultRows) >= sqlQueryMaxRows {
				truncated = true
				break
			}
			raw := rows.RawValues()
			row := make([]*string, len(raw))
			for i, b := range raw {
				if b == nil {
					row[i] = nil
					continue
				}
				s := string(b)
				row[i] = &s
			}
			resultRows = append(resultRows, row)
		}
		if err := rows.Err(); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": describePgError(err)})
			return
		}

		elapsedMs := time.Since(start).Milliseconds()

		writeJSON(w, http.StatusOK, sqlExecuteResponse{
			Columns:   columns,
			Rows:      resultRows,
			RowCount:  len(resultRows),
			Truncated: truncated,
			ElapsedMs: elapsedMs,
		})
	}
}

func describeVerdict(v *pgsqlarbiter.Verdict) string {
	parts := []string{}
	if v.DisallowedStatementType {
		parts = append(parts, "only SELECT statements are allowed")
	}
	if len(v.DisallowedTables) > 0 {
		parts = append(parts, "tables not allowed: "+strings.Join(v.DisallowedTables, ", "))
	}
	if len(v.DisallowedFunctions) > 0 {
		parts = append(parts, "functions not allowed: "+strings.Join(v.DisallowedFunctions, ", "))
	}
	if len(parts) == 0 {
		return "query rejected"
	}
	return strings.Join(parts, "; ")
}

func describePgError(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "57014":
			return "query timed out"
		case "25006":
			return "writes are not allowed in this query"
		}
		// The raw Postgres message can leak details about underlying tables,
		// internal schema, etc. Fall back to a generic message that still gives
		// the user enough context to look up the SQLSTATE if they want to.
		return fmt.Sprintf("query failed (SQLSTATE %s)", pgErr.Code)
	}
	return "query failed"
}

func typeNameForOID(typeMap *pgtype.Map, oid uint32) string {
	if t, ok := typeMap.TypeForOID(oid); ok {
		return t.Name
	}
	return fmt.Sprintf("oid %d", oid)
}

// ---------------------------------------------------------------------------
// Schema description
// ---------------------------------------------------------------------------

type sqlSchemaColumn struct {
	Name     string  `json:"name"`
	DataType string  `json:"data_type"`
	Comment  *string `json:"comment"`
}

type sqlSchemaView struct {
	Name    string            `json:"name"`
	Comment *string           `json:"comment"`
	Columns []sqlSchemaColumn `json:"columns"`
}

func handleGetSQLSchema(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rows, err := pool.Query(r.Context(), `
			SELECT
				c.relname,
				obj_description(c.oid, 'pg_class'),
				a.attname,
				format_type(a.atttypid, a.atttypmod),
				col_description(a.attrelid, a.attnum)
			FROM pg_class c
			JOIN pg_namespace n ON n.oid = c.relnamespace
			JOIN pg_attribute a ON a.attrelid = c.oid
			WHERE n.nspname = 'sql_query'
			  AND c.relkind = 'v'
			  AND a.attnum > 0
			  AND NOT a.attisdropped
			ORDER BY c.relname, a.attnum
		`)
		if err != nil {
			internalError(w, r, err)
			return
		}
		defer rows.Close()

		viewIndex := map[string]*sqlSchemaView{}
		views := []*sqlSchemaView{}

		for rows.Next() {
			var (
				viewName    string
				viewComment *string
				colName     string
				dataType    string
				colComment  *string
			)
			if err := rows.Scan(&viewName, &viewComment, &colName, &dataType, &colComment); err != nil {
				internalError(w, r, err)
				return
			}
			v, ok := viewIndex[viewName]
			if !ok {
				v = &sqlSchemaView{Name: viewName, Comment: viewComment, Columns: []sqlSchemaColumn{}}
				viewIndex[viewName] = v
				views = append(views, v)
			}
			v.Columns = append(v.Columns, sqlSchemaColumn{
				Name:     colName,
				DataType: dataType,
				Comment:  colComment,
			})
		}
		if err := rows.Err(); err != nil {
			internalError(w, r, err)
			return
		}

		writeJSON(w, http.StatusOK, map[string]any{"views": views})
	}
}

// ---------------------------------------------------------------------------
// Saved queries
// ---------------------------------------------------------------------------

type savedQueryRequest struct {
	Name      string `json:"name"`
	QueryText string `json:"query_text"`
}

type savedQueryResponse struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	QueryText string    `json:"query_text"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func validateSavedQueryRequest(req *savedQueryRequest) error {
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || len(req.Name) > savedQueryNameMaxLen {
		return errors.New("name must be 1-100 characters")
	}
	if strings.TrimSpace(req.QueryText) == "" {
		return errors.New("query_text is required")
	}
	if len(req.QueryText) > sqlQueryMaxLength {
		return errors.New("query_text is too long")
	}
	return nil
}

func handleListSavedQueries(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := userFromContext(r.Context())

		rows, err := pool.Query(r.Context(),
			`SELECT id, name, query_text, created_at, updated_at
			 FROM saved_sql_queries WHERE user_id = $1
			 ORDER BY lower(name)`,
			user.ID,
		)
		if err != nil {
			internalError(w, r, err)
			return
		}
		defer rows.Close()

		out := []savedQueryResponse{}
		for rows.Next() {
			var q savedQueryResponse
			if err := rows.Scan(&q.ID, &q.Name, &q.QueryText, &q.CreatedAt, &q.UpdatedAt); err != nil {
				internalError(w, r, err)
				return
			}
			out = append(out, q)
		}

		writeJSON(w, http.StatusOK, out)
	}
}

func handleCreateSavedQuery(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := userFromContext(r.Context())

		var req savedQueryRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}
		if err := validateSavedQueryRequest(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}

		var q savedQueryResponse
		err := pool.QueryRow(r.Context(),
			`INSERT INTO saved_sql_queries (user_id, name, query_text)
			 VALUES ($1, $2, $3)
			 RETURNING id, name, query_text, created_at, updated_at`,
			user.ID, req.Name, req.QueryText,
		).Scan(&q.ID, &q.Name, &q.QueryText, &q.CreatedAt, &q.UpdatedAt)

		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				writeJSON(w, http.StatusConflict, map[string]string{"error": "a saved query with that name already exists"})
				return
			}
			internalError(w, r, err)
			return
		}

		writeJSON(w, http.StatusCreated, q)
	}
}

func handleUpdateSavedQuery(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := userFromContext(r.Context())
		queryID := chi.URLParam(r, "id")

		var req savedQueryRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}
		if err := validateSavedQueryRequest(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}

		var q savedQueryResponse
		err := pool.QueryRow(r.Context(),
			`UPDATE saved_sql_queries
			 SET name = $1, query_text = $2, updated_at = now()
			 WHERE id = $3 AND user_id = $4
			 RETURNING id, name, query_text, created_at, updated_at`,
			req.Name, req.QueryText, queryID, user.ID,
		).Scan(&q.ID, &q.Name, &q.QueryText, &q.CreatedAt, &q.UpdatedAt)

		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "saved query not found"})
				return
			}
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				writeJSON(w, http.StatusConflict, map[string]string{"error": "a saved query with that name already exists"})
				return
			}
			internalError(w, r, err)
			return
		}

		writeJSON(w, http.StatusOK, q)
	}
}

func handleDeleteSavedQuery(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := userFromContext(r.Context())
		queryID := chi.URLParam(r, "id")

		tag, err := pool.Exec(r.Context(),
			`DELETE FROM saved_sql_queries WHERE id = $1 AND user_id = $2`,
			queryID, user.ID,
		)
		if err != nil {
			internalError(w, r, err)
			return
		}
		if tag.RowsAffected() == 0 {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "saved query not found"})
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
