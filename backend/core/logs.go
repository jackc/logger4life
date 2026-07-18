package core

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/logger4life/backend/domain"
)

var ErrUnauthenticated = errors.New("not authenticated")

type Log struct {
	ID           string                   `json:"id"`
	Name         string                   `json:"name"`
	Fields       []domain.FieldDefinition `json:"fields"`
	IsOwner      bool                     `json:"is_owner"`
	ShareToken   *string                  `json:"share_token,omitempty"`
	FolderID     *string                  `json:"folder_id"`
	Position     int                      `json:"position"`
	PinnedToHome bool                     `json:"pinned_to_home"`
	HomePosition int                      `json:"home_position"`
	CreatedAt    time.Time                `json:"created_at"`
	UpdatedAt    time.Time                `json:"updated_at"`
}

// LogStore is the driven persistence port for logs. Implementations belong in
// infrastructure packages and must enforce the supplied user's scope.
type LogStore interface {
	ListLogs(context.Context, string) ([]Log, error)
}

type ListLogsParams struct{}

var ListLogs = Define(ActionDef[ListLogsParams, []Log]{
	Name: "list_logs", Description: "List the logs visible to the current user.",
	Handler: func(ctx context.Context, c *Core, _ ListLogsParams) ([]Log, error) {
		userID, ok := UserIDFromContext(ctx)
		if !ok {
			return nil, ErrUnauthenticated
		}
		return c.logs.ListLogs(ctx, userID)
	},
})
