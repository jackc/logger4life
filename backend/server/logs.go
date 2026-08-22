package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/logger4life/backend/core"
	"github.com/jackc/logger4life/backend/domain"
)

type fieldDefinition = domain.FieldDefinition

type createLogRequest = core.CreateLogParams

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
	return domain.ValidateFieldDefinitions(fields)
}

func validateFieldValues(definitions []fieldDefinition, values map[string]any) error {
	return domain.ValidateFieldValues(definitions, values)
}

func handleCreateLog(app *core.Core) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := userFromContext(r.Context())

		var req createLogRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}

		l, err := core.CreateLog.Call(core.WithUserID(r.Context(), user.ID), app, req)
		if err != nil {
			var ve *core.ValidationError
			if errors.As(err, &ve) {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": ve.Err.Error()})
				return
			}
			if errors.Is(err, core.ErrLogNameTaken) {
				writeJSON(w, http.StatusConflict, map[string]string{"error": "a log with that name already exists"})
				return
			}
			internalError(w, r, err)
			return
		}

		writeJSON(w, http.StatusCreated, l)
	}
}

func handleListLogs(app *core.Core) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := userFromContext(r.Context())
		ctx := core.WithUserID(r.Context(), user.ID)
		logs, err := core.ListLogs.Call(ctx, app, core.ListLogsParams{})
		if err != nil {
			internalError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, logs)
	}
}

func handleGetLog(app *core.Core) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := userFromContext(r.Context())
		logID := chi.URLParam(r, "logID")
		l, err := core.GetLog.Call(core.WithUserID(r.Context(), user.ID), app, core.GetLogParams{LogID: logID})
		if err != nil {
			if errors.Is(err, core.ErrLogNotFound) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			} else {
				internalError(w, r, err)
			}
			return
		}
		writeJSON(w, http.StatusOK, l)
	}
}

func handleUpdateLog(app *core.Core) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := userFromContext(r.Context())
		logID := chi.URLParam(r, "logID")

		var req createLogRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}

		l, err := core.UpdateLog.Call(core.WithUserID(r.Context(), user.ID), app, core.UpdateLogParams{LogID: logID, Name: req.Name, Fields: req.Fields})
		if err != nil {
			var ve *core.ValidationError
			if errors.As(err, &ve) {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": ve.Err.Error()})
				return
			}
			if errors.Is(err, core.ErrLogNotFound) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "log not found"})
				return
			}
			if errors.Is(err, core.ErrLogNameTaken) {
				writeJSON(w, http.StatusConflict, map[string]string{"error": "a log with that name already exists"})
				return
			}
			internalError(w, r, err)
			return
		}

		writeJSON(w, http.StatusOK, l)
	}
}

func handleDeleteLog(app *core.Core) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := userFromContext(r.Context())
		logID := chi.URLParam(r, "logID")

		_, err := core.DeleteLog.Call(core.WithUserID(r.Context(), user.ID), app, core.DeleteLogParams{LogID: logID})
		if err != nil {
			if errors.Is(err, core.ErrLogNotFound) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "log not found"})
				return
			}
			internalError(w, r, err)
			return
		}

		w.WriteHeader(http.StatusNoContent)
	}
}

func writeLogOperationError(w http.ResponseWriter, r *http.Request, err error) {
	var ve *core.ValidationError
	switch {
	case errors.As(err, &ve):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": ve.Err.Error()})
	case errors.Is(err, core.ErrLogNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "log not found"})
	case errors.Is(err, core.ErrLogEntryNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "entry not found"})
	case errors.Is(err, core.ErrFolderNotFound):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "folder not found"})
	case errors.Is(err, core.ErrLogNotPinned):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	default:
		internalError(w, r, err)
	}
}

func handleCreateLogEntry(app *core.Core) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body createLogEntryRequest
		if !decodeAction(w, r, &body) {
			return
		}
		v, err := core.CreateLogEntry.Call(actionContext(r), app, core.CreateLogEntryParams{LogID: chi.URLParam(r, "logID"), Fields: body.Fields})
		if err != nil {
			writeLogOperationError(w, r, err)
			return
		}
		writeJSON(w, http.StatusCreated, v)
	}
}
func handleListLogEntries(app *core.Core) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		v, err := core.ListLogEntries.Call(actionContext(r), app, core.ListLogEntriesParams{LogID: chi.URLParam(r, "logID")})
		if err != nil {
			writeLogOperationError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, v)
	}
}
func handleUpdateLogEntry(app *core.Core) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body updateLogEntryRequest
		if !decodeAction(w, r, &body) {
			return
		}
		v, err := core.UpdateLogEntry.Call(actionContext(r), app, core.UpdateLogEntryParams{LogID: chi.URLParam(r, "logID"), EntryID: chi.URLParam(r, "entryID"), Fields: body.Fields, OccurredAt: body.OccurredAt})
		if err != nil {
			writeLogOperationError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, v)
	}
}
func handleDeleteLogEntry(app *core.Core) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, err := core.DeleteLogEntry.Call(actionContext(r), app, core.DeleteLogEntryParams{LogID: chi.URLParam(r, "logID"), EntryID: chi.URLParam(r, "entryID")})
		if err != nil {
			writeLogOperationError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
func handleUpdateLogPlacement(app *core.Core) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body updateLogPlacementRequest
		if !decodeAction(w, r, &body) {
			return
		}
		_, err := core.UpdateLogPlacement.Call(actionContext(r), app, core.UpdateLogPlacementParams{LogID: chi.URLParam(r, "logID"), FolderID: body.FolderID, Position: body.Position})
		if err != nil {
			writeLogOperationError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
func handlePinLog(app *core.Core) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body pinLogRequest
		if !decodeAction(w, r, &body) {
			return
		}
		_, err := core.PinLog.Call(actionContext(r), app, core.PinLogParams{LogID: chi.URLParam(r, "logID"), Pinned: body.Pinned})
		if err != nil {
			writeLogOperationError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
func handleUpdateLogHomePosition(app *core.Core) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body updateHomePositionRequest
		if !decodeAction(w, r, &body) {
			return
		}
		_, err := core.UpdateLogHomePosition.Call(actionContext(r), app, core.UpdateLogHomePositionParams{LogID: chi.URLParam(r, "logID"), HomePosition: body.HomePosition})
		if err != nil {
			writeLogOperationError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
