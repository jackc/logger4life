// Package core is Logger4Life's application service layer. It owns the
// action catalog and depends only on driven ports; HTTP, MCP, and PostgreSQL
// are adapters around it.
package core

import "context"

type Core struct {
	logs         LogStore
	entries      LogEntryStore
	placements   LogPlacementStore
	folders      FolderStore
	savedQueries SavedQueryStore
	sqlSchema    SQLSchemaStore
	sharing      SharingStore
	middleware   []Middleware
}

type Config struct {
	Logs         LogStore
	Entries      LogEntryStore
	Placements   LogPlacementStore
	Folders      FolderStore
	SavedQueries SavedQueryStore
	SQLSchema    SQLSchemaStore
	Sharing      SharingStore
	Middleware   []Middleware
}

func New(cfg Config) *Core {
	return &Core{logs: cfg.Logs, entries: cfg.Entries, placements: cfg.Placements, folders: cfg.Folders, savedQueries: cfg.SavedQueries, sqlSchema: cfg.SQLSchema, sharing: cfg.Sharing, middleware: append([]Middleware(nil), cfg.Middleware...)}
}
func (c *Core) run(ctx context.Context, inv Invocation, h Handler) (any, error) {
	for i := len(c.middleware) - 1; i >= 0; i-- {
		h = c.middleware[i](h)
	}
	return h(ctx, inv)
}

type contextKey uint8

const userIDKey contextKey = 1

func WithUserID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, userIDKey, id)
}
func UserIDFromContext(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(userIDKey).(string)
	return id, ok && id != ""
}
