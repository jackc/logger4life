package core

import (
	"context"
	"errors"
	"testing"
)

type fakeSQLSchemaStore struct {
	views []*SQLSchemaView
	err   error
	calls int
}

func (s *fakeSQLSchemaStore) ListSQLSchemaViews(_ context.Context) ([]*SQLSchemaView, error) {
	s.calls++
	return s.views, s.err
}

// get_sql_schema describes the read-only views every user queries through, not
// any user's data, so it takes no user scope. Authentication is the adapter's
// job: the HTTP route sits behind requireAuth and MCP behind a bearer token.
func TestGetSQLSchemaReturnsViewsWithoutUserScope(t *testing.T) {
	store := &fakeSQLSchemaStore{views: []*SQLSchemaView{{
		Name:    "logs",
		Comment: stringPointer("Logs visible to the current user"),
		Columns: []SQLSchemaColumn{{Name: "name", DataType: "text"}},
	}}}
	app := New(Config{SQLSchema: store})

	schema, err := GetSQLSchema.Call(context.Background(), app, GetSQLSchemaParams{})
	if err != nil {
		t.Fatal(err)
	}
	if len(schema.Views) != 1 || schema.Views[0].Name != "logs" {
		t.Fatalf("GetSQLSchema() = %#v", schema.Views)
	}
	if len(schema.Views[0].Columns) != 1 || schema.Views[0].Columns[0].Name != "name" {
		t.Fatalf("view columns = %#v", schema.Views[0].Columns)
	}
	if store.calls != 1 {
		t.Fatalf("store calls = %d, want 1", store.calls)
	}
}

func TestGetSQLSchemaPropagatesStoreErrors(t *testing.T) {
	sentinel := errors.New("schema unavailable")
	app := New(Config{SQLSchema: &fakeSQLSchemaStore{err: sentinel}})

	if _, err := GetSQLSchema.Call(context.Background(), app, GetSQLSchemaParams{}); !errors.Is(err, sentinel) {
		t.Fatalf("get_sql_schema error = %v, want the store error", err)
	}
}
