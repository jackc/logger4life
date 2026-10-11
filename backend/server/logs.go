package server

import (
	"errors"
	"net/http"
	"time"

	"github.com/jackc/logger4life/backend/core"
	"github.com/jackc/logger4life/backend/domain"
	"github.com/jackc/manifest/httpregistry"
)

type fieldDefinition = domain.FieldDefinition

type createLogRequest = core.CreateLogParams

type logResponse struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	Fields       []fieldDefinition `json:"fields"`
	IsOwner      bool              `json:"is_owner"`
	ShareToken   *string           `json:"share_token,omitzero"`
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

type updateLogEntryRequest struct {
	Note       *string        `json:"note"`
	Fields     map[string]any `json:"fields"`
	OccurredAt time.Time      `json:"occurred_at"`
}

type logEntryResponse struct {
	Note       string         `json:"note"`
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
		if err := decodeJSONObject(r.Body, &req); err != nil {
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

func handleUpdateLog(app *core.Core) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := userFromContext(r.Context())
		logID := httpregistry.PathParam(r, "logID")

		var req createLogRequest
		if err := decodeJSONObject(r.Body, &req); err != nil {
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
		logID := httpregistry.PathParam(r, "logID")

		_, err := core.DeleteLog.Call(core.WithUserID(r.Context(), user.ID), app, core.DeleteLogParams{LogID: logID})
		if err != nil {
			writeLogOperationError(w, r, err)
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

func handleListLogEntries(app *core.Core) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		v, err := core.ListLogEntries.Call(actionContext(r), app, core.ListLogEntriesParams{LogID: httpregistry.PathParam(r, "logID")})
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
		v, err := core.UpdateLogEntry.Call(actionContext(r), app, core.UpdateLogEntryParams{LogID: httpregistry.PathParam(r, "logID"), EntryID: httpregistry.PathParam(r, "entryID"), Fields: body.Fields, Note: body.Note, OccurredAt: body.OccurredAt})
		if err != nil {
			writeLogOperationError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, v)
	}
}

func handleDeleteLogEntry(app *core.Core) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, err := core.DeleteLogEntry.Call(actionContext(r), app, core.DeleteLogEntryParams{LogID: httpregistry.PathParam(r, "logID"), EntryID: httpregistry.PathParam(r, "entryID")})
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
		_, err := core.UpdateLogPlacement.Call(actionContext(r), app, core.UpdateLogPlacementParams{LogID: httpregistry.PathParam(r, "logID"), FolderID: body.FolderID, Position: body.Position})
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
		_, err := core.PinLog.Call(actionContext(r), app, core.PinLogParams{LogID: httpregistry.PathParam(r, "logID"), Pinned: body.Pinned})
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
		_, err := core.UpdateLogHomePosition.Call(actionContext(r), app, core.UpdateLogHomePositionParams{LogID: httpregistry.PathParam(r, "logID"), HomePosition: body.HomePosition})
		if err != nil {
			writeLogOperationError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
