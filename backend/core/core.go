// Package core is Logger4Life's application service layer. It owns the
// action catalog and depends only on driven ports; HTTP, MCP, and PostgreSQL
// are adapters around it.
package core

import (
	"context"

	"github.com/go-webauthn/webauthn/webauthn"
)

type Core struct {
	users        UserStore
	sessions     SessionStore
	passkeys     PasskeyStore
	challenges   PasskeyChallengeStore
	webAuthn     *webauthn.WebAuthn
	tx           Transactor
	logs         LogStore
	entries      LogEntryStore
	placements   LogPlacementStore
	folders      FolderStore
	savedQueries SavedQueryStore
	sqlSchema    SQLSchemaStore
	userSQL      UserSQLExecutor
	sharing      SharingStore
	middleware   []Middleware
}

type Config struct {
	Users        UserStore
	Sessions     SessionStore
	Passkeys     PasskeyStore
	Challenges   PasskeyChallengeStore
	WebAuthn     *webauthn.WebAuthn
	Tx           Transactor
	Logs         LogStore
	Entries      LogEntryStore
	Placements   LogPlacementStore
	Folders      FolderStore
	SavedQueries SavedQueryStore
	SQLSchema    SQLSchemaStore
	UserSQL      UserSQLExecutor
	Sharing      SharingStore
	Middleware   []Middleware
}

func New(cfg Config) *Core {
	tx := cfg.Tx
	if tx == nil {
		tx = passthroughTx{}
	}
	return &Core{users: cfg.Users, sessions: cfg.Sessions, passkeys: cfg.Passkeys, challenges: cfg.Challenges, webAuthn: cfg.WebAuthn, tx: tx, logs: cfg.Logs, entries: cfg.Entries, placements: cfg.Placements, folders: cfg.Folders, savedQueries: cfg.SavedQueries, sqlSchema: cfg.SQLSchema, userSQL: cfg.UserSQL, sharing: cfg.Sharing, middleware: append([]Middleware(nil), cfg.Middleware...)}
}

// Transactor is the driven port for transaction boundaries. Store calls made
// with the context passed to fn participate in the same transaction.
type Transactor interface {
	InTx(context.Context, func(context.Context) error) error
}

type passthroughTx struct{}

func (passthroughTx) InTx(ctx context.Context, fn func(context.Context) error) error {
	return fn(ctx)
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
