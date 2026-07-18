package backend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/logger4life/backend/core"
	"github.com/jackc/logger4life/backend/pgstore"
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
	Columns   []sqlColumn `json:"columns"`
	Rows      [][]*string `json:"rows"`
	RowCount  int         `json:"row_count"`
	Truncated bool        `json:"truncated"`
	ElapsedMs int64       `json:"elapsed_ms"`
}

// userSQLError is a query-execution failure caused by the user's input
// (bad SQL, arbiter rejection, timeout, etc.). The Message is safe to
// surface to the caller; non-userSQLError errors are internal and should
// be logged but never echoed verbatim.
type userSQLError struct {
	Message string
}

func (e *userSQLError) Error() string { return e.Message }

func newUserSQLError(msg string) *userSQLError { return &userSQLError{Message: msg} }

// executeUserSQL runs the given query as the calling user against the
// sql_query schema views, returning columns and rows. It enforces the
// arbiter, the read-only restricted role, per-user view filtering, and
// the row cap. Returns *userSQLError for user-facing failures (400-class)
// and a plain error for internal failures (500-class).
func executeUserSQL(ctx context.Context, pool *pgxpool.Pool, arbiter *pgsqlarbiter.Arbiter, userID, query string) (sqlExecuteResponse, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return sqlExecuteResponse{}, newUserSQLError("query is required")
	}
	if len(query) > sqlQueryMaxLength {
		return sqlExecuteResponse{}, newUserSQLError("query is too long")
	}

	verdict, err := arbiter.Judge(query)
	if err != nil {
		return sqlExecuteResponse{}, newUserSQLError(err.Error())
	}
	if !verdict.Allowed {
		return sqlExecuteResponse{}, newUserSQLError(describeVerdict(verdict))
	}

	conn, err := pool.Acquire(ctx)
	if err != nil {
		return sqlExecuteResponse{}, err
	}
	defer conn.Release()

	tx, err := conn.BeginTx(ctx, pgx.TxOptions{
		IsoLevel:   pgx.ReadCommitted,
		AccessMode: pgx.ReadOnly,
	})
	if err != nil {
		return sqlExecuteResponse{}, err
	}
	defer tx.Rollback(ctx)

	// Configure session as the privileged role, then drop into the restricted
	// role. set_config(..., true) is transaction-local, equivalent to SET LOCAL.
	setup := []string{
		"SET LOCAL statement_timeout = '" + sqlQueryStatementTimeout + "'",
		"SET LOCAL idle_in_transaction_session_timeout = '" + sqlQueryIdleTimeout + "'",
	}
	for _, stmt := range setup {
		if _, err := tx.Exec(ctx, stmt); err != nil {
			return sqlExecuteResponse{}, err
		}
	}
	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_user_id', $1, true)", userID); err != nil {
		return sqlExecuteResponse{}, err
	}
	if _, err := tx.Exec(ctx, "SET LOCAL ROLE logger4life_sql_user"); err != nil {
		return sqlExecuteResponse{}, err
	}
	if _, err := tx.Exec(ctx, "SET LOCAL search_path TO sql_query"); err != nil {
		return sqlExecuteResponse{}, err
	}

	start := time.Now()

	// pgx.QueryExecModeExec uses the extended protocol (which forbids
	// multi-statement queries) and skips the prepared-statement cache.
	// A single-element QueryResultFormats applies that format to every column.
	rows, err := tx.Query(ctx, query,
		pgx.QueryExecModeExec,
		pgx.QueryResultFormats{pgx.TextFormatCode},
	)
	if err != nil {
		return sqlExecuteResponse{}, newUserSQLError(describePgError(err))
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
		return sqlExecuteResponse{}, newUserSQLError(describePgError(err))
	}

	return sqlExecuteResponse{
		Columns:   columns,
		Rows:      resultRows,
		RowCount:  len(resultRows),
		Truncated: truncated,
		ElapsedMs: time.Since(start).Milliseconds(),
	}, nil
}

func handleExecuteSQL(pool *pgxpool.Pool, arbiter *pgsqlarbiter.Arbiter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := userFromContext(r.Context())

		var req sqlExecuteRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}

		result, err := executeUserSQL(r.Context(), pool, arbiter, user.ID, req.Query)
		if err != nil {
			var userErr *userSQLError
			if errors.As(err, &userErr) {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": userErr.Message})
				return
			}
			internalError(w, r, err)
			return
		}

		writeJSON(w, http.StatusOK, result)
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

func listSQLSchemaViews(ctx context.Context, pool *pgxpool.Pool) ([]*sqlSchemaView, error) {
	rows, err := pool.Query(ctx, `
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
		return nil, err
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
			return nil, err
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
		return nil, err
	}
	return views, nil
}

func handleGetSQLSchema(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		views, err := listSQLSchemaViews(r.Context(), pool)
		if err != nil {
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

func listSavedQueriesForUser(ctx context.Context, pool *pgxpool.Pool, userID string) ([]savedQueryResponse, error) {
	rows, err := pool.Query(ctx,
		`SELECT id, name, query_text, created_at, updated_at
		 FROM saved_sql_queries WHERE user_id = $1
		 ORDER BY lower(name)`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []savedQueryResponse{}
	for rows.Next() {
		var q savedQueryResponse
		if err := rows.Scan(&q.ID, &q.Name, &q.QueryText, &q.CreatedAt, &q.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	return out, rows.Err()
}

// getSavedQueryByName returns the user's saved query with the given name.
// Returns pgx.ErrNoRows if not found.
func getSavedQueryByName(ctx context.Context, pool *pgxpool.Pool, userID, name string) (savedQueryResponse, error) {
	var q savedQueryResponse
	err := pool.QueryRow(ctx,
		`SELECT id, name, query_text, created_at, updated_at
		 FROM saved_sql_queries
		 WHERE user_id = $1 AND name = $2`,
		userID, name,
	).Scan(&q.ID, &q.Name, &q.QueryText, &q.CreatedAt, &q.UpdatedAt)
	return q, err
}

func savedQueryCore(pool *pgxpool.Pool, configured []*core.Core) *core.Core {
	if len(configured) > 0 {
		return configured[0]
	}
	return core.New(core.Config{SavedQueries: pgstore.New(pool)})
}
func writeSavedQueryError(w http.ResponseWriter, r *http.Request, e error) {
	var ve *core.ValidationError
	switch {
	case errors.As(e, &ve):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": ve.Err.Error()})
	case errors.Is(e, core.ErrSavedQueryNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": e.Error()})
	case errors.Is(e, core.ErrSavedQueryNameTaken):
		writeJSON(w, http.StatusConflict, map[string]string{"error": e.Error()})
	default:
		internalError(w, r, e)
	}
}

func handleListSavedQueries(pool *pgxpool.Pool, configured ...*core.Core) http.HandlerFunc {
	app := savedQueryCore(pool, configured)
	return func(w http.ResponseWriter, r *http.Request) {
		user := userFromContext(r.Context())
		out, err := core.ListSavedQueries.Call(core.WithUserID(r.Context(), user.ID), app, core.ListSavedQueriesParams{})
		if err != nil {
			internalError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, out)
	}
}

func handleCreateSavedQuery(pool *pgxpool.Pool, configured ...*core.Core) http.HandlerFunc {
	app := savedQueryCore(pool, configured)
	return func(w http.ResponseWriter, r *http.Request) {
		user := userFromContext(r.Context())

		var req savedQueryRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}
		q, err := core.CreateSavedQuery.Call(core.WithUserID(r.Context(), user.ID), app, core.SavedQueryParams{Name: req.Name, QueryText: req.QueryText})
		if err != nil {
			writeSavedQueryError(w, r, err)
			return
		}

		writeJSON(w, http.StatusCreated, q)
	}
}

func handleUpdateSavedQuery(pool *pgxpool.Pool, configured ...*core.Core) http.HandlerFunc {
	app := savedQueryCore(pool, configured)
	return func(w http.ResponseWriter, r *http.Request) {
		user := userFromContext(r.Context())
		queryID := chi.URLParam(r, "id")

		var req savedQueryRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}
		q, err := core.UpdateSavedQuery.Call(core.WithUserID(r.Context(), user.ID), app, core.UpdateSavedQueryParams{ID: queryID, Name: req.Name, QueryText: req.QueryText})
		if err != nil {
			writeSavedQueryError(w, r, err)
			return
		}

		writeJSON(w, http.StatusOK, q)
	}
}

func handleDeleteSavedQuery(pool *pgxpool.Pool, configured ...*core.Core) http.HandlerFunc {
	app := savedQueryCore(pool, configured)
	return func(w http.ResponseWriter, r *http.Request) {
		user := userFromContext(r.Context())
		queryID := chi.URLParam(r, "id")

		_, err := core.DeleteSavedQuery.Call(core.WithUserID(r.Context(), user.ID), app, core.DeleteSavedQueryParams{ID: queryID})
		if err != nil {
			writeSavedQueryError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
