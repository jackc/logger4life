package core

import (
	"context"

	"github.com/jackc/manifest/action"
)

var ErrUnknownAction = action.ErrUnknownAction

type (
	ValidationError = action.ValidationError
	Invocation      = action.Invocation[ActionMetadata]
	Handler         = action.Handler[ActionMetadata]
	Middleware      = action.Middleware[ActionMetadata]
	AnyAction       = action.AnyAction[*Core, ActionMetadata]
)

// ActionMetadata contains Logger4Life's action policies. The action package
// carries these values without interpreting them; application middleware owns
// authentication requirements and mutation auditing.
type ActionMetadata struct {
	Mutating bool
	// Public marks an action a caller may invoke before being anyone.
	Public bool
}

// ActionDef keeps application declarations independent of the generic
// catalog's dependency and metadata type parameters.
type ActionDef[P, R any] = action.Definition[*Core, ActionMetadata, P, R]

// Action is a catalog action. Call and InvokeJSON run it through the
// dispatcher of the *Core they are given.
type Action[P, R any] = action.CatalogAction[*Core, ActionMetadata, P, R]

var catalog = action.NewCatalog(action.CatalogOptions[*Core, ActionMetadata]{
	Dispatcher: func(c *Core) *action.Dispatcher[*Core, ActionMetadata] { return c.actions },
})

func Define[P, R any](def ActionDef[P, R]) *Action[P, R] { return catalog.Define(def) }

func Lookup(name string) (AnyAction, bool) { return catalog.Lookup(name) }
func Catalog() []AnyAction                 { return catalog.Actions() }

func (c *Core) InvokeJSON(ctx context.Context, name string, data []byte) (any, error) {
	return catalog.InvokeJSON(ctx, c, name, data)
}
