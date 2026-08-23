package jedstore

import (
	"context"

	"github.com/jackc/logger4life/backend/core"
)

func stringPtr(s string) *string { return &s }

// jed intentionally has no PostgreSQL system catalogs or COMMENT metadata.
// This is the stable public schema of the two filtered views created by the
// embedded migration, so describe it at the adapter boundary.
func (s *Store) ListSQLSchemaViews(context.Context) ([]*core.SQLSchemaView, error) {
	return []*core.SQLSchemaView{
		{
			Name: "log_entries", Comment: stringPtr("Entries from logs you own or have been shared on."),
			Columns: []core.SQLSchemaColumn{
				{Name: "id", DataType: "uuid", Comment: stringPtr("UUID identifying the entry.")},
				{Name: "log_id", DataType: "uuid", Comment: stringPtr("UUID of the parent log (join to logs.id).")},
				{Name: "user_id", DataType: "uuid", Comment: stringPtr("UUID of the user who created the entry.")},
				{Name: "user_username", DataType: "character varying(30)", Comment: stringPtr("Username of the user who created the entry.")},
				{Name: "fields", DataType: "jsonb", Comment: stringPtr("JSONB object with the entry's field values, keyed by field name.")},
				{Name: "occurred_at", DataType: "timestamp with time zone", Comment: stringPtr("When the event being logged occurred.")},
				{Name: "created_at", DataType: "timestamp with time zone", Comment: stringPtr("When the entry record was created.")},
				{Name: "updated_at", DataType: "timestamp with time zone", Comment: stringPtr("When the entry record was last updated.")},
			},
		},
		{
			Name: "logs", Comment: stringPtr("Logs you own or have been shared on."),
			Columns: []core.SQLSchemaColumn{
				{Name: "id", DataType: "uuid", Comment: stringPtr("UUID identifying the log.")},
				{Name: "name", DataType: "character varying(100)", Comment: stringPtr("Display name of the log.")},
				{Name: "fields", DataType: "jsonb", Comment: stringPtr("JSONB array of field definitions: [{\"name\",\"type\",\"required\"}].")},
				{Name: "created_at", DataType: "timestamp with time zone", Comment: stringPtr("When the log was created.")},
				{Name: "updated_at", DataType: "timestamp with time zone", Comment: stringPtr("When the log was last updated.")},
				{Name: "shared_with", DataType: "text[]", Comment: stringPtr("Array of usernames the log is shared with. NULL unless you are the log owner.")},
			},
		},
	}, nil
}
