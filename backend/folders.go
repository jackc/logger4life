package backend

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/logger4life/backend/core"
)

type folderResponse = core.Folder
type createFolderRequest = core.CreateFolderParams
type renameFolderRequest struct {
	Name string `json:"name"`
}
type moveFolderRequest struct {
	ParentFolderID *string `json:"parent_folder_id"`
	Position       int     `json:"position"`
}

func actionContext(r *http.Request) context.Context {
	return core.WithUserID(r.Context(), userFromContext(r.Context()).ID)
}
func decodeAction(w http.ResponseWriter, r *http.Request, v any) bool {
	if json.NewDecoder(r.Body).Decode(v) != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return false
	}
	return true
}
func writeFolderError(w http.ResponseWriter, r *http.Request, e error) {
	var ve *core.ValidationError
	switch {
	case errors.As(e, &ve):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": ve.Err.Error()})
	case errors.Is(e, core.ErrFolderNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": e.Error()})
	case errors.Is(e, core.ErrFolderNotEmpty):
		writeJSON(w, http.StatusConflict, map[string]string{"error": e.Error()})
	case errors.Is(e, core.ErrParentFolderNotFound), errors.Is(e, core.ErrFolderCycle), errors.Is(e, core.ErrFolderOwnParent):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": e.Error()})
	default:
		internalError(w, r, e)
	}
}

func handleCreateFolder(app *core.Core) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var p core.CreateFolderParams
		if !decodeAction(w, r, &p) {
			return
		}
		f, e := core.CreateFolder.Call(actionContext(r), app, p)
		if e != nil {
			writeFolderError(w, r, e)
			return
		}
		writeJSON(w, http.StatusCreated, f)
	}
}
func handleListFolders(app *core.Core) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		v, e := core.ListFolders.Call(actionContext(r), app, core.ListFoldersParams{})
		if e != nil {
			writeFolderError(w, r, e)
			return
		}
		writeJSON(w, http.StatusOK, v)
	}
}
func handleRenameFolder(app *core.Core) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body renameFolderRequest
		if !decodeAction(w, r, &body) {
			return
		}
		v, e := core.RenameFolder.Call(actionContext(r), app, core.RenameFolderParams{FolderID: chi.URLParam(r, "folderID"), Name: body.Name})
		if e != nil {
			writeFolderError(w, r, e)
			return
		}
		writeJSON(w, http.StatusOK, v)
	}
}
func handleMoveFolder(app *core.Core) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body moveFolderRequest
		if !decodeAction(w, r, &body) {
			return
		}
		_, e := core.MoveFolder.Call(actionContext(r), app, core.MoveFolderParams{FolderID: chi.URLParam(r, "folderID"), ParentFolderID: body.ParentFolderID, Position: body.Position})
		if e != nil {
			writeFolderError(w, r, e)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
func handleDeleteFolder(app *core.Core) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, e := core.DeleteFolder.Call(actionContext(r), app, core.DeleteFolderParams{FolderID: chi.URLParam(r, "folderID")})
		if e != nil {
			writeFolderError(w, r, e)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
