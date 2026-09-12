package core

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/logger4life/backend/domain"
)

const MaxCollectionPageBytes = 256 << 10

// CollectionStore fetches only limit rows after the supplied (folded name,
// ID) key, in byte order, scoped to the supplied user. SortName must come
// from the database's lower(name), matching the query's ordering exactly.
type CollectionStore interface {
	ListLogsPage(context.Context, string, string, string, int) ([]LogSummary, error)
	ListSavedQueriesPage(context.Context, string, string, string, int) ([]SavedQuerySummary, error)
}

type LogSummary struct {
	ID        string                   `json:"id"`
	Name      string                   `json:"name"`
	Fields    []domain.FieldDefinition `json:"fields"`
	IsOwner   bool                     `json:"is_owner"`
	CreatedAt time.Time                `json:"created_at"`
	UpdatedAt time.Time                `json:"updated_at"`
	SortName  string                   `json:"-"`
}

type SavedQuerySummary struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	QueryText string    `json:"query_text"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	SortName  string    `json:"-"`
}

type CollectionPageParams struct {
	Limit  int    `json:"limit,omitempty" jsonschema:"maximum records to return; defaults to 50, maximum 100; byte limits may shorten a page"`
	Cursor string `json:"cursor,omitempty" jsonschema:"opaque next_cursor from the previous page of this tool"`
}

func (p *CollectionPageParams) Validate() error {
	if p.Limit == 0 {
		p.Limit = 50
	}
	if p.Limit < 1 || p.Limit > 100 {
		return errors.New("limit must be between 1 and 100")
	}
	if len(p.Cursor) > 1024 {
		return errors.New("invalid cursor")
	}
	return nil
}

type collectionCursor struct {
	Version    int    `json:"v"`
	Collection string `json:"collection"`
	UserID     string `json:"user"`
	Name       string `json:"name"`
	ID         string `json:"id"`
}

func decodeCollectionCursor(value, collection, user string) (collectionCursor, error) {
	if value == "" {
		return collectionCursor{Version: 1, Collection: collection, UserID: user}, nil
	}
	var cursor collectionCursor
	b, err := base64.RawURLEncoding.DecodeString(value)
	if err == nil {
		err = json.Unmarshal(b, &cursor)
	}
	if err != nil || cursor.Version != 1 || cursor.Collection != collection || cursor.UserID != user || cursor.ID == "" || len(cursor.ID) > 128 || len(cursor.Name) > 400 {
		return collectionCursor{}, &ValidationError{Err: errors.New("invalid cursor")}
	}
	return cursor, nil
}

// boundCollectionPage reserves space for the envelope and cursor. Each
// item is encoded once; cutting a page never advances past an omitted row.
func boundCollectionPage[T any](all []T, limit int, cursor collectionCursor, key func(T) (string, string)) ([]T, string, error) {
	selected := make([]T, 0, min(limit, len(all)))
	size := 2048
	for _, item := range all {
		if len(selected) == limit {
			break
		}
		encoded, err := json.Marshal(item)
		if err != nil {
			return nil, "", err
		}
		if size+len(encoded)+1 > MaxCollectionPageBytes {
			if len(selected) == 0 {
				return nil, "", &ValidationError{Err: errors.New("a record exceeds the collection page size limit")}
			}
			break
		}
		size += len(encoded) + 1
		selected = append(selected, item)
	}
	next := ""
	if len(selected) < len(all) {
		cursor.Name, cursor.ID = key(selected[len(selected)-1])
		encoded, err := json.Marshal(cursor)
		if err != nil {
			return nil, "", err
		}
		next = base64.RawURLEncoding.EncodeToString(encoded)
	}
	return selected, next, nil
}

type LogPage struct {
	Logs       []LogSummary `json:"logs"`
	NextCursor string       `json:"next_cursor,omitempty"`
}

type SavedQueryPage struct {
	Queries    []SavedQuerySummary `json:"queries"`
	NextCursor string              `json:"next_cursor,omitempty"`
}

var ListLogsPage = Define(ActionDef[CollectionPageParams, LogPage]{Name: "list_logs_page", Description: "List a bounded alphabetical page of visible logs.", Handler: func(ctx context.Context, c *Core, p CollectionPageParams) (LogPage, error) {
	user, err := requiredUser(ctx)
	if err != nil {
		return LogPage{}, err
	}
	cursor, err := decodeCollectionCursor(p.Cursor, "logs", user)
	if err != nil {
		return LogPage{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	rows, err := c.collections.ListLogsPage(ctx, user, cursor.Name, cursor.ID, p.Limit+1)
	if err != nil {
		return LogPage{}, err
	}
	logs, next, err := boundCollectionPage(rows, p.Limit, cursor, func(l LogSummary) (string, string) { return l.SortName, l.ID })
	return LogPage{Logs: logs, NextCursor: next}, err
}})

var ListSavedQueriesPage = Define(ActionDef[CollectionPageParams, SavedQueryPage]{Name: "list_saved_queries_page", Description: "List a bounded alphabetical page of saved SQL queries.", Handler: func(ctx context.Context, c *Core, p CollectionPageParams) (SavedQueryPage, error) {
	user, err := requiredUser(ctx)
	if err != nil {
		return SavedQueryPage{}, err
	}
	cursor, err := decodeCollectionCursor(p.Cursor, "queries", user)
	if err != nil {
		return SavedQueryPage{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	rows, err := c.collections.ListSavedQueriesPage(ctx, user, cursor.Name, cursor.ID, p.Limit+1)
	if err != nil {
		return SavedQueryPage{}, err
	}
	queries, next, err := boundCollectionPage(rows, p.Limit, cursor, func(q SavedQuerySummary) (string, string) { return q.SortName, q.ID })
	return SavedQueryPage{Queries: queries, NextCursor: next}, err
}})
