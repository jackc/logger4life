package core

import (
	"context"

	"github.com/jackc/logger4life/action"
)

var ErrUnknownAction = action.ErrUnknownAction

type ValidationError = action.ValidationError
type Invocation = action.Invocation[ActionMetadata]
type Handler = action.Handler[ActionMetadata]
type Middleware = action.Middleware[ActionMetadata]
type AnyAction = action.AnyAction[*Core, ActionMetadata]

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

type Action[P, R any] struct {
	*action.Action[*Core, ActionMetadata, P, R]
}

var registry action.Registry[*Core, ActionMetadata]

func Define[P, R any](def ActionDef[P, R]) *Action[P, R] {
	return &Action[P, R]{registry.Define(def)}
}

func Lookup(name string) (AnyAction, bool) { return registry.Lookup(name) }
func Catalog() []AnyAction                 { return registry.Catalog() }

func (a *Action[P, R]) Call(ctx context.Context, c *Core, p P) (R, error) {
	return a.Action.Call(ctx, c.actions, c, p)
}

func (c *Core) InvokeJSON(ctx context.Context, name string, data []byte) (any, error) {
	return c.actions.InvokeJSON(ctx, c, name, data)
}
