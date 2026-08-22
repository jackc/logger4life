package server

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/logger4life/backend/core"
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

func handleGetSQLSchema(app *core.Core) http.HandlerFunc {
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

func handleListSavedQueries(app *core.Core) http.HandlerFunc {
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

func handleCreateSavedQuery(app *core.Core) http.HandlerFunc {
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

func handleUpdateSavedQuery(app *core.Core) http.HandlerFunc {
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

func handleDeleteSavedQuery(app *core.Core) http.HandlerFunc {
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
