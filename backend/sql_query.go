package backend

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/logger4life/backend/core"
	"github.com/jackc/logger4life/backend/pgstore"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	sqlQueryMaxLength    = 10000
	savedQueryNameMaxLen = 100
)

func handleExecuteSQL(app *core.Core) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var params core.ExecuteUserSQLParams
		if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}
		result, err := core.ExecuteUserSQL.Call(actionContext(r), app, params)
		if err != nil {
			writeUserSQLError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func writeUserSQLError(w http.ResponseWriter, r *http.Request, err error) {
	var validationErr *core.ValidationError
	var queryFailure *core.UserSQLFailure
	switch {
	case errors.As(err, &validationErr):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": validationErr.Err.Error()})
	case errors.As(err, &queryFailure):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": queryFailure.Error()})
	case errors.Is(err, core.ErrUnauthenticated):
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
	default:
		internalError(w, r, err)
	}
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

func handleGetSQLSchema(pool *pgxpool.Pool, configured ...*core.Core) http.HandlerFunc {
	app := core.New(core.Config{SQLSchema: pgstore.New(pool)})
	if len(configured) > 0 {
		app = configured[0]
	}
	return func(w http.ResponseWriter, r *http.Request) {
		result, err := core.GetSQLSchema.Call(r.Context(), app, core.GetSQLSchemaParams{})
		if err != nil {
			internalError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
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
