// Package core is Logger4Life's application service layer. It owns the
// action catalog and depends only on driven ports; HTTP, MCP, and PostgreSQL
// are adapters around it.
package core

import "context"

type Core struct {
	logs       LogStore
	folders    FolderStore
	middleware []Middleware
}

type Config struct {
	Logs       LogStore
	Folders    FolderStore
	Middleware []Middleware
}

func New(cfg Config) *Core {
	return &Core{logs: cfg.Logs, folders: cfg.Folders, middleware: append([]Middleware(nil), cfg.Middleware...)}
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
