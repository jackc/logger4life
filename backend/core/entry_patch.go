package core

import (
	"context"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/jackc/logger4life/backend/domain"
)

type GetLogEntryParams struct {
	LogID   string `json:"log_id"`
	EntryID string `json:"entry_id"`
}

func (p *GetLogEntryParams) Validate() error {
	if err := validID("log_id", p.LogID); err != nil {
		return err
	}
	return validID("entry_id", p.EntryID)
}

var GetLogEntry = Define(ActionDef[GetLogEntryParams, domain.LogEntry]{Name: "get_log_entry", Description: "Read an entry in a visible log.", Handler: func(ctx context.Context, c *Core, p GetLogEntryParams) (domain.LogEntry, error) {
	u, err := requiredUser(ctx)
	if err != nil {
		return domain.LogEntry{}, err
	}
	return c.entries.GetLogEntry(ctx, u, p.LogID, p.EntryID, false)
}})

type PatchLogEntryParams struct {
	LogID      string         `json:"log_id"`
	EntryID    string         `json:"entry_id"`
	Fields     map[string]any `json:"fields,omitempty" jsonschema:"field changes only; omitted fields are preserved; null removes an optional field; number values must be strings"`
	OccurredAt *time.Time     `json:"occurred_at,omitempty" jsonschema:"RFC3339 timestamp with timezone; omitted preserves current timestamp"`
	Note       *string        `json:"note,omitempty" jsonschema:"replacement Markdown note; omitted preserves current note; empty string clears it"`
}

func (p *PatchLogEntryParams) Validate() error {
	ids := GetLogEntryParams{LogID: p.LogID, EntryID: p.EntryID}
	if err := ids.Validate(); err != nil {
		return err
	}
	if p.OccurredAt != nil && p.OccurredAt.IsZero() {
		return fmt.Errorf("occurred_at must be a nonzero timestamp")
	}
	if p.Note != nil && utf8.RuneCountInString(*p.Note) > 20000 {
		return fmt.Errorf("note must be at most 20000 characters")
	}
	return nil
}

var PatchLogEntry = Define(ActionDef[PatchLogEntryParams, domain.LogEntry]{Name: "patch_log_entry", Description: "Atomically update only supplied entry fields, timestamp, or note.", Metadata: ActionMetadata{Mutating: true}, Handler: func(ctx context.Context, c *Core, p PatchLogEntryParams) (domain.LogEntry, error) {
	u, err := requiredUser(ctx)
	if err != nil {
		return domain.LogEntry{}, err
	}
	var result domain.LogEntry
	err = c.tx.InTx(ctx, func(ctx context.Context) error {
		entry, err := c.entries.GetLogEntry(ctx, u, p.LogID, p.EntryID, true)
		if err != nil {
			return err
		}
		fields := make(map[string]any, len(entry.Fields)+len(p.Fields))
		for k, v := range entry.Fields {
			fields[k] = v
		}
		for k, v := range p.Fields {
			if v == nil {
				delete(fields, k)
			} else {
				fields[k] = v
			}
		}
		defs, err := c.entries.LogFieldDefinitions(ctx, u, p.LogID)
		if err != nil {
			return err
		}
		// Validate supplied names even when a null would remove them.
		known := map[string]bool{}
		for _, d := range defs {
			known[d.Name] = true
		}
		for k := range p.Fields {
			if !known[k] {
				return &ValidationError{Err: fmt.Errorf("unknown field: %s", k)}
			}
		}
		if err := domain.ValidateFieldValues(defs, fields); err != nil {
			return &ValidationError{Err: err}
		}
		occurred := entry.OccurredAt
		if p.OccurredAt != nil {
			occurred = p.OccurredAt.UTC().Truncate(time.Microsecond)
		}
		result, err = c.entries.UpdateLogEntry(ctx, u, p.LogID, p.EntryID, fields, occurred, p.Note)
		return err
	})
	return result, err
}})
