package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/logger4life/backend/domain"
)

var (
	ErrUnauthenticated = errors.New("not authenticated")
	ErrLogNameTaken    = errors.New("a log with that name already exists")
)

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
	CreateLog(context.Context, string, string, []domain.FieldDefinition) (Log, error)
	ListLogs(context.Context, string) ([]Log, error)
}

type CreateLogParams struct {
	Name   string                   `json:"name"`
	Fields []domain.FieldDefinition `json:"fields"`
}

func (p *CreateLogParams) Validate() error {
	p.Name = strings.TrimSpace(p.Name)
	if len(p.Name) < 1 || len(p.Name) > 100 {
		return fmt.Errorf("name must be 1-100 characters")
	}
	if p.Fields == nil {
		p.Fields = []domain.FieldDefinition{}
	}
	return domain.ValidateFieldDefinitions(p.Fields)
}

var CreateLog = Define(ActionDef[CreateLogParams, Log]{Name: "create_log", Description: "Create a log and its initial home placement.", Mutating: true, Handler: func(ctx context.Context, c *Core, p CreateLogParams) (Log, error) {
	id, e := requiredUser(ctx)
	if e != nil {
		return Log{}, e
	}
	return c.logs.CreateLog(ctx, id, p.Name, p.Fields)
}})

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
