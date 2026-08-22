package server

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/logger4life/backend/core"
)

func writeSharingError(w http.ResponseWriter, r *http.Request, err error) {
	var ve *core.ValidationError
	switch {
	case errors.As(err, &ve):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": ve.Err.Error()})
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

func handleCreateShareToken(app *core.Core) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		result, err := core.CreateShareToken.Call(actionContext(r), app, core.CreateShareTokenParams{LogID: chi.URLParam(r, "logID")})
		if err != nil {
			writeSharingError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func handleDeleteShareToken(app *core.Core) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, err := core.DeleteShareToken.Call(actionContext(r), app, core.DeleteShareTokenParams{LogID: chi.URLParam(r, "logID")})
		if err != nil {
			writeSharingError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func handleListShares(app *core.Core) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		shares, err := core.ListSharedUsers.Call(actionContext(r), app, core.ListSharedUsersParams{LogID: chi.URLParam(r, "logID")})
		if err != nil {
			writeSharingError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, shares)
	}
}

func handleRemoveShare(app *core.Core) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, err := core.RemoveSharedUser.Call(actionContext(r), app, core.RemoveSharedUserParams{LogID: chi.URLParam(r, "logID"), ShareID: chi.URLParam(r, "shareID")})
		if err != nil {
			writeSharingError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func handleGetShareInfo(app *core.Core) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		info, err := core.GetShareInfo.Call(actionContext(r), app, core.GetShareInfoParams{Token: chi.URLParam(r, "token")})
		if err != nil {
			writeSharingError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, info)
	}
}

func handleJoinLog(app *core.Core) http.HandlerFunc {
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
