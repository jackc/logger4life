package server

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"time"

	"github.com/jackc/logger4life/backend/core"
	"github.com/jackc/logger4life/backend/domain"
	"github.com/jackc/manifest/mcpregistry"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type mcpRetry struct {
	IdempotencyKey string `json:"idempotency_key,omitempty" jsonschema:"optional retry key, at most 128 bytes; reuse with identical arguments to receive the original result without repeating the write"`
}

func (p mcpRetry) retryKey() string { return p.IdempotencyKey }

type mcpDeletion struct {
	ID      string `json:"id"`
	LogID   string `json:"log_id,omitempty"`
	Deleted bool   `json:"deleted"`
}
type mcpFolders struct {
	Folders []core.Folder `json:"folders"`
}
type mcpSharedUsers struct {
	Users []core.SharedUser `json:"users"`
}

func registerMCPRead[P, R any](r *mcpregistry.Registry[MCPMetadata], name, title, description string, fn func(context.Context, P) (R, error)) {
	r.Register(mcpregistry.Spec[MCPMetadata]{Tool: mcp.Tool{Name: name, Title: title, Description: description, Annotations: mcpregistry.ReadOnlyAnnotations()}, Metadata: MCPMetadata{RequiresUser: true}}, mcpregistry.Typed(func(ctx context.Context, _ *mcp.CallToolRequest, p P) (*mcp.CallToolResult, R, error) {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		result, err := fn(ctx, p)
		if err == nil {
			data, e := json.Marshal(result)
			err = e
			if len(data) > core.MaxCollectionPageBytes {
				err = &core.ValidationError{Err: fmt.Errorf("result exceeds response size limit")}
			}
		}
		if err != nil {
			return nil, result, mcpToolError(ctx, err)
		}
		return nil, result, nil
	}))
}
func registerMCPWrite[P interface{ retryKey() string }, R any](r *mcpregistry.Registry[MCPMetadata], app *core.Core, name, title, description string, destructive bool, fn func(context.Context, P) (R, error)) {
	closed := false
	r.Register(mcpregistry.Spec[MCPMetadata]{Tool: mcp.Tool{Name: name, Title: title, Description: description, Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: &destructive, IdempotentHint: false, OpenWorldHint: &closed}}, Metadata: MCPMetadata{RequiresUser: true}}, mcpregistry.Typed(func(ctx context.Context, _ *mcp.CallToolRequest, p P) (*mcp.CallToolResult, R, error) {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		result, err := core.RunWrite(ctx, app, name, p.retryKey(), p, func(ctx context.Context) (R, error) {
			result, err := fn(ctx, p)
			if err != nil {
				return result, err
			}
			data, err := json.Marshal(result)
			if err == nil && len(data) > core.MaxCollectionPageBytes {
				err = &core.ValidationError{Err: fmt.Errorf("result exceeds response size limit")}
			}
			return result, err
		})
		if err != nil {
			return nil, result, mcpToolError(ctx, err)
		}
		return nil, result, nil
	}))
}
func mcpGetFolder(ctx context.Context, app *core.Core, id string) (core.Folder, error) {
	folders, err := core.ListFolders.Call(ctx, app, core.ListFoldersParams{})
	if err != nil {
		return core.Folder{}, err
	}
	for _, f := range folders {
		if f.ID == id {
			return f, nil
		}
	}
	return core.Folder{}, core.ErrFolderNotFound
}

type mcpCreateLogEntryInput struct {
	core.CreateLogEntryParams
	mcpRetry
}

type mcpPatchLogEntryInput struct {
	core.PatchLogEntryParams
	mcpRetry
}

type mcpDeleteLogEntryInput struct {
	core.DeleteLogEntryParams
	mcpRetry
}

type mcpCreateLogInput struct {
	core.CreateLogParams
	mcpRetry
}

type mcpUpdateLogInput struct {
	core.UpdateLogParams
	mcpRetry
}

type mcpDeleteLogInput struct {
	core.DeleteLogParams
	mcpRetry
}

type mcpCreateFolderInput struct {
	core.CreateFolderParams
	mcpRetry
}

type mcpRenameFolderInput struct {
	core.RenameFolderParams
	mcpRetry
}

type mcpMoveFolderInput struct {
	core.MoveFolderParams
	mcpRetry
}

type mcpDeleteFolderInput struct {
	core.DeleteFolderParams
	mcpRetry
}

type mcpUpdateLogPlacementInput struct {
	core.UpdateLogPlacementParams
	mcpRetry
}

type mcpPinLogInput struct {
	core.PinLogParams
	mcpRetry
}

type mcpUpdateLogHomePositionInput struct {
	core.UpdateLogHomePositionParams
	mcpRetry
}

type mcpCreateShareTokenInput struct {
	core.CreateShareTokenParams
	mcpRetry
}

type mcpDeleteShareTokenInput struct {
	core.DeleteShareTokenParams
	mcpRetry
}

type mcpJoinSharedLogInput struct {
	core.JoinSharedLogParams
	mcpRetry
}

type mcpRemoveSharedUserInput struct {
	core.RemoveSharedUserParams
	mcpRetry
}

func registerMCPActions(r *mcpregistry.Registry[MCPMetadata], app *core.Core) {
	registerMCPWrite(r, app, "create_log_entry", "Create log entry", "Create an entry with optional Markdown note and RFC3339 occurred_at (defaults to now). Number field values must be strings.", false, func(ctx context.Context, p mcpCreateLogEntryInput) (domain.LogEntry, error) {
		return core.CreateLogEntry.Call(ctx, app, p.CreateLogEntryParams)
	})
	registerMCPWrite(r, app, "update_log_entry", "Update log entry", "Patch an entry: omitted fields, timestamp, and note are preserved. Fields are merged; null removes an optional field. Empty note clears it. Number values must be strings.", true, func(ctx context.Context, p mcpPatchLogEntryInput) (domain.LogEntry, error) {
		return core.PatchLogEntry.Call(ctx, app, p.PatchLogEntryParams)
	})
	registerMCPWrite(r, app, "delete_log_entry", "Delete log entry", "Delete an entry in an owned or shared log. Returns a deletion receipt.", true, func(ctx context.Context, p mcpDeleteLogEntryInput) (mcpDeletion, error) {
		_, err := core.DeleteLogEntry.Call(ctx, app, p.DeleteLogEntryParams)
		return mcpDeletion{ID: p.EntryID, LogID: p.LogID, Deleted: err == nil}, err
	})
	registerMCPWrite(r, app, "create_log", "Create log", "Create a log with text, number, or boolean field definitions and its initial home placement.", false, func(ctx context.Context, p mcpCreateLogInput) (core.Log, error) {
		return core.CreateLog.Call(ctx, app, p.CreateLogParams)
	})
	registerMCPWrite(r, app, "update_log", "Update log", "Replace an owned log name and field definitions. Supply the complete field definition list. Historical entries are not migrated.", true, func(ctx context.Context, p mcpUpdateLogInput) (core.Log, error) {
		return core.UpdateLog.Call(ctx, app, p.UpdateLogParams)
	})
	registerMCPWrite(r, app, "delete_log", "Delete log", "Delete an owned log and all its entries and sharing. Returns a deletion receipt.", true, func(ctx context.Context, p mcpDeleteLogInput) (mcpDeletion, error) {
		_, err := core.DeleteLog.Call(ctx, app, p.DeleteLogParams)
		return mcpDeletion{ID: p.LogID, Deleted: err == nil}, err
	})
	registerMCPWrite(r, app, "create_folder", "Create folder", "Create a folder under parent_folder_id, or at root when null.", false, func(ctx context.Context, p mcpCreateFolderInput) (core.Folder, error) {
		return core.CreateFolder.Call(ctx, app, p.CreateFolderParams)
	})
	registerMCPWrite(r, app, "rename_folder", "Rename folder", "Rename a folder owned by the caller.", true, func(ctx context.Context, p mcpRenameFolderInput) (core.Folder, error) {
		return core.RenameFolder.Call(ctx, app, p.RenameFolderParams)
	})
	registerMCPWrite(r, app, "move_folder", "Move folder", "Move a folder under a parent (null for root) and set its position. Returns the resulting folder.", true, func(ctx context.Context, p mcpMoveFolderInput) (core.Folder, error) {
		_, err := core.MoveFolder.Call(ctx, app, p.MoveFolderParams)
		if err != nil {
			return core.Folder{}, err
		}
		return mcpGetFolder(ctx, app, p.FolderID)
	})
	registerMCPWrite(r, app, "delete_folder", "Delete folder", "Delete an empty folder. Nonempty folders are rejected. Returns a deletion receipt.", true, func(ctx context.Context, p mcpDeleteFolderInput) (mcpDeletion, error) {
		_, err := core.DeleteFolder.Call(ctx, app, p.DeleteFolderParams)
		return mcpDeletion{ID: p.FolderID, Deleted: err == nil}, err
	})
	registerMCPWrite(r, app, "update_log_placement", "Update log placement", "Move a visible log into a folder (null for root) and set its position for the caller. Returns the resulting log with placement.", true, func(ctx context.Context, p mcpUpdateLogPlacementInput) (core.Log, error) {
		_, err := core.UpdateLogPlacement.Call(ctx, app, p.UpdateLogPlacementParams)
		if err != nil {
			return core.Log{}, err
		}
		return core.GetLog.Call(ctx, app, core.GetLogParams{LogID: p.LogID})
	})
	registerMCPWrite(r, app, "pin_log", "Pin log", "Pin or unpin a visible log on the caller home screen. Returns the resulting log with placement.", true, func(ctx context.Context, p mcpPinLogInput) (core.Log, error) {
		_, err := core.PinLog.Call(ctx, app, p.PinLogParams)
		if err != nil {
			return core.Log{}, err
		}
		return core.GetLog.Call(ctx, app, core.GetLogParams{LogID: p.LogID})
	})
	registerMCPWrite(r, app, "update_log_home_position", "Update log home position", "Reorder a pinned log on the caller home screen. Returns the resulting log with placement.", true, func(ctx context.Context, p mcpUpdateLogHomePositionInput) (core.Log, error) {
		_, err := core.UpdateLogHomePosition.Call(ctx, app, p.UpdateLogHomePositionParams)
		if err != nil {
			return core.Log{}, err
		}
		return core.GetLog.Call(ctx, app, core.GetLogParams{LogID: p.LogID})
	})
	registerMCPWrite(r, app, "create_share_token", "Create share token", "Create or rotate the invitation token of an owned log. Returns the log including share_token. Rotation replaces the previous invitation token.", true, func(ctx context.Context, p mcpCreateShareTokenInput) (core.Log, error) {
		_, err := core.CreateShareToken.Call(ctx, app, p.CreateShareTokenParams)
		if err != nil {
			return core.Log{}, err
		}
		return core.GetLog.Call(ctx, app, core.GetLogParams{LogID: p.LogID})
	})
	registerMCPWrite(r, app, "delete_share_token", "Delete share token", "Revoke the invitation token of an owned log. Existing members keep access. Returns the resulting log.", true, func(ctx context.Context, p mcpDeleteShareTokenInput) (core.Log, error) {
		_, err := core.DeleteShareToken.Call(ctx, app, p.DeleteShareTokenParams)
		if err != nil {
			return core.Log{}, err
		}
		return core.GetLog.Call(ctx, app, core.GetLogParams{LogID: p.LogID})
	})
	registerMCPWrite(r, app, "join_shared_log", "Join shared log", "Join a log using its invitation token. Returns the joined log with the caller placement.", false, func(ctx context.Context, p mcpJoinSharedLogInput) (core.Log, error) {
		joined, err := core.JoinSharedLog.Call(ctx, app, p.JoinSharedLogParams)
		if err != nil {
			return core.Log{}, err
		}
		return core.GetLog.Call(ctx, app, core.GetLogParams{LogID: joined.LogID})
	})
	registerMCPWrite(r, app, "remove_shared_user", "Remove shared user", "Remove a membership from an owned log. share_id is the membership id returned by list_shared_users, not the user account ID.", true, func(ctx context.Context, p mcpRemoveSharedUserInput) (mcpDeletion, error) {
		_, err := core.RemoveSharedUser.Call(ctx, app, p.RemoveSharedUserParams)
		return mcpDeletion{ID: p.ShareID, LogID: p.LogID, Deleted: err == nil}, err
	})
	registerMCPRead(r, "get_log", "Get log", "Read a visible log including fields, caller folder placement, pin state, and owner-only invitation token.", func(ctx context.Context, p core.GetLogParams) (core.Log, error) { return core.GetLog.Call(ctx, app, p) })
	registerMCPRead(r, "get_log_entry", "Get log entry", "Read one entry by log_id and entry_id, including field values, timestamps, and Markdown note.", func(ctx context.Context, p core.GetLogEntryParams) (domain.LogEntry, error) {
		return core.GetLogEntry.Call(ctx, app, p)
	})
	registerMCPRead(r, "list_folders", "List folders", "List the caller folders including parent IDs and positions.", func(ctx context.Context, p core.ListFoldersParams) (mcpFolders, error) {
		v, e := core.ListFolders.Call(ctx, app, p)
		return mcpFolders{Folders: v}, e
	})
	registerMCPRead(r, "get_folder", "Get folder", "Read a folder by folder_id.", func(ctx context.Context, p core.DeleteFolderParams) (core.Folder, error) {
		if err := p.Validate(); err != nil {
			return core.Folder{}, &core.ValidationError{Err: err}
		}
		return mcpGetFolder(ctx, app, p.FolderID)
	})
	registerMCPRead(r, "list_shared_users", "List shared users", "List memberships of an owned log. Each id is a share_id usable by remove_shared_user.", func(ctx context.Context, p core.ListSharedUsersParams) (mcpSharedUsers, error) {
		v, e := core.ListSharedUsers.Call(ctx, app, p)
		return mcpSharedUsers{Users: v}, e
	})
	registerMCPRead(r, "get_share_info", "Get share info", "Preview an invitation token before joining a shared log.", func(ctx context.Context, p core.GetShareInfoParams) (core.ShareInfo, error) {
		return core.GetShareInfo.Call(ctx, app, p)
	})
}
