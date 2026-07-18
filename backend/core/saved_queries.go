package core

import (
	"context"
	"errors"
	"strings"
	"time"
)

var (
	ErrSavedQueryNotFound  = errors.New("saved query not found")
	ErrSavedQueryNameTaken = errors.New("a saved query with that name already exists")
)

type SavedQuery struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	QueryText string    `json:"query_text"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
type SavedQueryStore interface {
	ListSavedQueries(context.Context, string) ([]SavedQuery, error)
	GetSavedQueryByName(context.Context, string, string) (SavedQuery, error)
	CreateSavedQuery(context.Context, string, string, string) (SavedQuery, error)
	UpdateSavedQuery(context.Context, string, string, string, string) (SavedQuery, error)
	DeleteSavedQuery(context.Context, string, string) error
}
type SavedQueryParams struct {
	Name      string `json:"name"`
	QueryText string `json:"query_text"`
}

func (p *SavedQueryParams) Validate() error {
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" || len(p.Name) > 100 {
		return errors.New("name must be 1-100 characters")
	}
	if strings.TrimSpace(p.QueryText) == "" {
		return errors.New("query_text is required")
	}
	if len(p.QueryText) > 10000 {
		return errors.New("query_text is too long")
	}
	return nil
}

type ListSavedQueriesParams struct{}

var ListSavedQueries = Define(ActionDef[ListSavedQueriesParams, []SavedQuery]{Name: "list_saved_queries", Description: "List saved SQL queries.", Handler: func(ctx context.Context, c *Core, _ ListSavedQueriesParams) ([]SavedQuery, error) {
	id, e := requiredUser(ctx)
	if e != nil {
		return nil, e
	}
	return c.savedQueries.ListSavedQueries(ctx, id)
}})

type GetSavedQueryParams struct {
	Name string `json:"name"`
}

var GetSavedQuery = Define(ActionDef[GetSavedQueryParams, SavedQuery]{Name: "get_saved_query", Description: "Get a saved SQL query by name.", Handler: func(ctx context.Context, c *Core, p GetSavedQueryParams) (SavedQuery, error) {
	id, e := requiredUser(ctx)
	if e != nil {
		return SavedQuery{}, e
	}
	return c.savedQueries.GetSavedQueryByName(ctx, id, p.Name)
}})
var CreateSavedQuery = Define(ActionDef[SavedQueryParams, SavedQuery]{Name: "create_saved_query", Description: "Create a saved SQL query.", Mutating: true, Handler: func(ctx context.Context, c *Core, p SavedQueryParams) (SavedQuery, error) {
	id, e := requiredUser(ctx)
	if e != nil {
		return SavedQuery{}, e
	}
	return c.savedQueries.CreateSavedQuery(ctx, id, p.Name, p.QueryText)
}})

type UpdateSavedQueryParams struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	QueryText string `json:"query_text"`
}

func (p *UpdateSavedQueryParams) Validate() error {
	v := SavedQueryParams{Name: p.Name, QueryText: p.QueryText}
	e := v.Validate()
	p.Name = v.Name
	return e
}

var UpdateSavedQuery = Define(ActionDef[UpdateSavedQueryParams, SavedQuery]{Name: "update_saved_query", Description: "Update a saved SQL query.", Mutating: true, Handler: func(ctx context.Context, c *Core, p UpdateSavedQueryParams) (SavedQuery, error) {
	id, e := requiredUser(ctx)
	if e != nil {
		return SavedQuery{}, e
	}
	return c.savedQueries.UpdateSavedQuery(ctx, id, p.ID, p.Name, p.QueryText)
}})

type DeleteSavedQueryParams struct {
	ID string `json:"id"`
}

var DeleteSavedQuery = Define(ActionDef[DeleteSavedQueryParams, struct{}]{Name: "delete_saved_query", Description: "Delete a saved SQL query.", Mutating: true, Handler: func(ctx context.Context, c *Core, p DeleteSavedQueryParams) (struct{}, error) {
	id, e := requiredUser(ctx)
	if e == nil {
		e = c.savedQueries.DeleteSavedQuery(ctx, id, p.ID)
	}
	return struct{}{}, e
}})
