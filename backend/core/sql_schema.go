package core

import "context"

type SQLSchemaColumn struct {
	Name     string  `json:"name"`
	DataType string  `json:"data_type"`
	Comment  *string `json:"comment"`
}
type SQLSchemaView struct {
	Name    string            `json:"name"`
	Comment *string           `json:"comment"`
	Columns []SQLSchemaColumn `json:"columns"`
}
type SQLSchemaStore interface {
	ListSQLSchemaViews(context.Context) ([]*SQLSchemaView, error)
}
type GetSQLSchemaParams struct{}
type SQLSchema struct {
	Views []*SQLSchemaView `json:"views"`
}

var GetSQLSchema = Define(ActionDef[GetSQLSchemaParams, SQLSchema]{Name: "get_sql_schema", Public: true, Description: "Describe the read-only SQL schema.", Handler: func(ctx context.Context, c *Core, _ GetSQLSchemaParams) (SQLSchema, error) {
	v, e := c.sqlSchema.ListSQLSchemaViews(ctx)
	return SQLSchema{Views: v}, e
}})
