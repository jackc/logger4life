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
	ErrLogNotFound     = errors.New("log not found")
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
	GetLog(context.Context, string, string) (Log, error)
	UpdateLog(context.Context, string, string, string, []domain.FieldDefinition) (Log, error)
	DeleteLog(context.Context, string, string) error
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

type GetLogParams struct {
	LogID string `json:"log_id"`
}

func (p *GetLogParams) Validate() error { return validID("log_id", p.LogID) }

var GetLog = Define(ActionDef[GetLogParams, Log]{Name: "get_log", Description: "Get a visible log.", Handler: func(ctx context.Context, c *Core, p GetLogParams) (Log, error) {
	id, e := requiredUser(ctx)
	if e != nil {
		return Log{}, e
	}
	return c.logs.GetLog(ctx, id, p.LogID)
}})

type UpdateLogParams struct {
	LogID  string                   `json:"log_id"`
	Name   string                   `json:"name"`
	Fields []domain.FieldDefinition `json:"fields"`
}

func (p *UpdateLogParams) Validate() error {
	if e := validID("log_id", p.LogID); e != nil {
		return e
	}
	v := CreateLogParams{Name: p.Name, Fields: p.Fields}
	e := v.Validate()
	p.Name = v.Name
	p.Fields = v.Fields
	return e
}

var UpdateLog = Define(ActionDef[UpdateLogParams, Log]{Name: "update_log", Description: "Update an owned log.", Mutating: true, Handler: func(ctx context.Context, c *Core, p UpdateLogParams) (Log, error) {
	id, e := requiredUser(ctx)
	if e != nil {
		return Log{}, e
	}
	return c.logs.UpdateLog(ctx, id, p.LogID, p.Name, p.Fields)
}})

type DeleteLogParams struct {
	LogID string `json:"log_id"`
}

func (p *DeleteLogParams) Validate() error { return validID("log_id", p.LogID) }

var DeleteLog = Define(ActionDef[DeleteLogParams, struct{}]{Name: "delete_log", Description: "Delete an owned log.", Mutating: true, Handler: func(ctx context.Context, c *Core, p DeleteLogParams) (struct{}, error) {
	id, e := requiredUser(ctx)
	if e == nil {
		e = c.logs.DeleteLog(ctx, id, p.LogID)
	}
	return struct{}{}, e
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
