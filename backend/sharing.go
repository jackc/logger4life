package backend

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/logger4life/backend/core"
	"github.com/jackc/logger4life/backend/pgstore"
	"github.com/jackc/pgx/v5/pgxpool"
)

func sharingCore(pool *pgxpool.Pool, configured []*core.Core) *core.Core {
	if len(configured) > 0 {
		return configured[0]
	}
	return core.New(core.Config{Sharing: pgstore.New(pool)})
}

func writeSharingError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, core.ErrLogNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "log not found"})
	case errors.Is(err, core.ErrShareNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "share not found"})
	case errors.Is(err, core.ErrInvalidShareLink):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "invalid share link"})
	case errors.Is(err, core.ErrAlreadyOwnLog):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "you already own this log"})
	default:
		internalError(w, r, err)
	}
}

func handleCreateShareToken(pool *pgxpool.Pool, configured ...*core.Core) http.HandlerFunc {
	app := sharingCore(pool, configured)
	return func(w http.ResponseWriter, r *http.Request) {
		result, err := core.CreateShareToken.Call(actionContext(r), app, core.CreateShareTokenParams{LogID: chi.URLParam(r, "logID")})
		if err != nil {
			writeSharingError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func handleDeleteShareToken(pool *pgxpool.Pool, configured ...*core.Core) http.HandlerFunc {
	app := sharingCore(pool, configured)
	return func(w http.ResponseWriter, r *http.Request) {
		_, err := core.DeleteShareToken.Call(actionContext(r), app, core.DeleteShareTokenParams{LogID: chi.URLParam(r, "logID")})
		if err != nil {
			writeSharingError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func handleListShares(pool *pgxpool.Pool, configured ...*core.Core) http.HandlerFunc {
	app := sharingCore(pool, configured)
	return func(w http.ResponseWriter, r *http.Request) {
		shares, err := core.ListSharedUsers.Call(actionContext(r), app, core.ListSharedUsersParams{LogID: chi.URLParam(r, "logID")})
		if err != nil {
			writeSharingError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, shares)
	}
}

func handleRemoveShare(pool *pgxpool.Pool, configured ...*core.Core) http.HandlerFunc {
	app := sharingCore(pool, configured)
	return func(w http.ResponseWriter, r *http.Request) {
		_, err := core.RemoveSharedUser.Call(actionContext(r), app, core.RemoveSharedUserParams{LogID: chi.URLParam(r, "logID"), ShareID: chi.URLParam(r, "shareID")})
		if err != nil {
			writeSharingError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func handleGetShareInfo(pool *pgxpool.Pool, configured ...*core.Core) http.HandlerFunc {
	app := sharingCore(pool, configured)
	return func(w http.ResponseWriter, r *http.Request) {
		info, err := core.GetShareInfo.Call(actionContext(r), app, core.GetShareInfoParams{Token: chi.URLParam(r, "token")})
		if err != nil {
			writeSharingError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, info)
	}
}

func handleJoinLog(pool *pgxpool.Pool, configured ...*core.Core) http.HandlerFunc {
	app := sharingCore(pool, configured)
	return func(w http.ResponseWriter, r *http.Request) {
		result, err := core.JoinSharedLog.Call(actionContext(r), app, core.JoinSharedLogParams{Token: chi.URLParam(r, "token")})
		if err != nil {
			writeSharingError(w, r, err)
			return
		}
		status := http.StatusCreated
		if result.AlreadyMember {
			status = http.StatusOK
		}
		writeJSON(w, status, result)
	}
}
