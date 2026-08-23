package core

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/logger4life/backend/domain"
)

var ErrLogEntryNotFound = errors.New("entry not found")

type LogEntryStore interface {
	LogFieldDefinitions(context.Context, string, string) ([]domain.FieldDefinition, error)
	CreateLogEntry(context.Context, string, string, string, map[string]any, time.Time) (domain.LogEntry, error)
	ListLogEntries(context.Context, string, string) ([]domain.LogEntry, error)
	UpdateLogEntry(context.Context, string, string, string, map[string]any, time.Time) (domain.LogEntry, error)
	DeleteLogEntry(context.Context, string, string, string) error
}

type CreateLogEntryParams struct {
	LogID  string         `json:"log_id"`
	Fields map[string]any `json:"fields"`
}

func (p *CreateLogEntryParams) Validate() error {
	if e := validID("log_id", p.LogID); e != nil {
		return e
	}
	if p.Fields == nil {
		p.Fields = map[string]any{}
	}
	return nil
}

var CreateLogEntry = Define(ActionDef[CreateLogEntryParams, domain.LogEntry]{Name: "create_log_entry", Description: "Create an entry in a visible log.", Mutating: true, Handler: func(ctx context.Context, c *Core, p CreateLogEntryParams) (domain.LogEntry, error) {
	u, e := requiredUser(ctx)
	if e != nil {
		return domain.LogEntry{}, e
	}
	defs, e := c.entries.LogFieldDefinitions(ctx, u, p.LogID)
	if e != nil {
		return domain.LogEntry{}, e
	}
	if e = domain.ValidateFieldValues(defs, p.Fields); e != nil {
		return domain.LogEntry{}, &ValidationError{Action: "create_log_entry", Err: e}
	}
	entryID, e := newID()
	if e != nil {
		return domain.LogEntry{}, e
	}
	occurredAt := time.Now().UTC().Truncate(time.Microsecond)
	return c.entries.CreateLogEntry(ctx, entryID, u, p.LogID, p.Fields, occurredAt)
}})

type ListLogEntriesParams struct {
	LogID string `json:"log_id"`
}

func (p *ListLogEntriesParams) Validate() error { return validID("log_id", p.LogID) }

var ListLogEntries = Define(ActionDef[ListLogEntriesParams, []domain.LogEntry]{Name: "list_log_entries", Description: "List entries in a visible log.", Handler: func(ctx context.Context, c *Core, p ListLogEntriesParams) ([]domain.LogEntry, error) {
	u, e := requiredUser(ctx)
	if e != nil {
		return nil, e
	}
	return c.entries.ListLogEntries(ctx, u, p.LogID)
}})

type UpdateLogEntryParams struct {
	LogID      string         `json:"log_id"`
	EntryID    string         `json:"entry_id"`
	Fields     map[string]any `json:"fields"`
	OccurredAt time.Time      `json:"occurred_at"`
}

func (p *UpdateLogEntryParams) Validate() error {
	if e := validID("log_id", p.LogID); e != nil {
		return e
	}
	if e := validID("entry_id", p.EntryID); e != nil {
		return e
	}
	if p.Fields == nil {
		p.Fields = map[string]any{}
	}
	if p.OccurredAt.IsZero() {
		return fmt.Errorf("occurred_at is required")
	}
	return nil
}

var UpdateLogEntry = Define(ActionDef[UpdateLogEntryParams, domain.LogEntry]{Name: "update_log_entry", Description: "Update an entry in a visible log.", Mutating: true, Handler: func(ctx context.Context, c *Core, p UpdateLogEntryParams) (domain.LogEntry, error) {
	u, e := requiredUser(ctx)
	if e != nil {
		return domain.LogEntry{}, e
	}
	defs, e := c.entries.LogFieldDefinitions(ctx, u, p.LogID)
	if e != nil {
		return domain.LogEntry{}, e
	}
	if e = domain.ValidateFieldValues(defs, p.Fields); e != nil {
		return domain.LogEntry{}, &ValidationError{Action: "update_log_entry", Err: e}
	}
	return c.entries.UpdateLogEntry(ctx, u, p.LogID, p.EntryID, p.Fields, p.OccurredAt)
}})

type DeleteLogEntryParams struct {
	LogID   string `json:"log_id"`
	EntryID string `json:"entry_id"`
}

func (p *DeleteLogEntryParams) Validate() error {
	if e := validID("log_id", p.LogID); e != nil {
		return e
	}
	return validID("entry_id", p.EntryID)
}

var DeleteLogEntry = Define(ActionDef[DeleteLogEntryParams, struct{}]{Name: "delete_log_entry", Description: "Delete an entry from a visible log.", Mutating: true, Handler: func(ctx context.Context, c *Core, p DeleteLogEntryParams) (struct{}, error) {
	u, e := requiredUser(ctx)
	if e == nil {
		e = c.entries.DeleteLogEntry(ctx, u, p.LogID, p.EntryID)
	}
	return struct{}{}, e
}})
