package core

import (
	"context"
	"crypto/sha256"
	"encoding/json/v2"
	"fmt"
	"time"

	"github.com/jackc/logger4life/backend/domain"
)

type EntryPageParams struct {
	CollectionPageParams
	LogID   string     `json:"log_id"`
	StartAt *time.Time `json:"start_at,omitempty" jsonschema:"inclusive occurred_at lower bound, RFC3339 with timezone"`
	EndAt   *time.Time `json:"end_at,omitempty" jsonschema:"exclusive occurred_at upper bound, RFC3339 with timezone"`
	Order   string     `json:"order,omitempty" jsonschema:"asc or desc by occurred_at then ID; defaults to desc (newest first)"`
}

func (p *EntryPageParams) Validate() error {
	if err := validID("log_id", p.LogID); err != nil {
		return err
	}
	if err := p.CollectionPageParams.Validate(); err != nil {
		return err
	}
	if p.Order == "" {
		p.Order = "desc"
	}
	if p.Order != "asc" && p.Order != "desc" {
		return fmt.Errorf("order must be asc or desc")
	}
	for _, bound := range []**time.Time{&p.StartAt, &p.EndAt} {
		if *bound != nil {
			if (*bound).IsZero() {
				return fmt.Errorf("time bounds must be nonzero timestamps")
			}
			normalized := (*bound).UTC()
			*bound = &normalized
		}
	}
	if p.StartAt != nil && p.EndAt != nil && !p.StartAt.Before(*p.EndAt) {
		return fmt.Errorf("start_at must be before end_at")
	}
	return nil
}

// EntryPageQuery is a bounded keyset query. Stores enforce visibility on every page.
type EntryPageQuery struct {
	LogID          string
	StartAt, EndAt *time.Time
	AfterAt        *time.Time
	AfterID        string
	Order          string
	Limit          int
}

type EntryPage struct {
	Entries    []domain.LogEntry `json:"entries"`
	NextCursor string            `json:"next_cursor,omitempty"`
}

var ListLogEntriesPage = Define(ActionDef[EntryPageParams, EntryPage]{Name: "list_log_entries_page", Description: "List a bounded page of entries in a visible log.", Handler: func(ctx context.Context, c *Core, p EntryPageParams) (EntryPage, error) {
	user, err := requiredUser(ctx)
	if err != nil {
		return EntryPage{}, err
	}
	// Bind cursors to the log, time window, direction, and authenticated user.
	// Page size may change between requests.
	scope, err := json.Marshal(struct {
		LogID          string
		StartAt, EndAt *time.Time
		Order          string
	}{p.LogID, p.StartAt, p.EndAt, p.Order})
	if err != nil {
		return EntryPage{}, err
	}
	cursor, err := decodeCollectionCursor(p.Cursor, fmt.Sprintf("entries:%x", sha256.Sum256(scope)), user)
	if err != nil {
		return EntryPage{}, err
	}
	query := EntryPageQuery{LogID: p.LogID, StartAt: p.StartAt, EndAt: p.EndAt, Order: p.Order, Limit: p.Limit + 1}
	if cursor.ID != "" {
		stamp, err := time.Parse(time.RFC3339Nano, cursor.Name)
		if err != nil || stamp.IsZero() || validID("cursor id", cursor.ID) != nil {
			return EntryPage{}, &ValidationError{Err: fmt.Errorf("invalid cursor")}
		}
		query.AfterAt, query.AfterID = &stamp, cursor.ID
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	rows, err := c.collections.ListLogEntriesPage(ctx, user, query)
	if err != nil {
		return EntryPage{}, err
	}
	entries, next, err := boundCollectionPage(rows, p.Limit, cursor, func(e domain.LogEntry) (string, string) { return e.OccurredAt.UTC().Format(time.RFC3339Nano), e.ID })
	return EntryPage{Entries: entries, NextCursor: next}, err
}})
