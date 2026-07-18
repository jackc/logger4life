package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
)

var ErrUnknownAction = errors.New("unknown action")

type ValidationError struct {
	Action string
	Err    error
}

func (e *ValidationError) Error() string { return fmt.Sprintf("%s: %v", e.Action, e.Err) }
func (e *ValidationError) Unwrap() error { return e.Err }

type Invocation struct {
	Action AnyAction
	Params any
}
type Handler func(context.Context, Invocation) (any, error)
type Middleware func(Handler) Handler

type AnyAction interface {
	Name() string
	Description() string
	Mutating() bool
	NewParams() any
	Invoke(context.Context, *Core, any) (any, error)
}

type ActionDef[P, R any] struct {
	Name        string
	Description string
	Mutating    bool
	Handler     func(context.Context, *Core, P) (R, error)
}

type Action[P, R any] struct{ def ActionDef[P, R] }

var registry = map[string]AnyAction{}
var registryMu sync.RWMutex

func Define[P, R any](def ActionDef[P, R]) *Action[P, R] {
	if def.Name == "" || def.Handler == nil {
		panic("core: action name and handler are required")
	}
	a := &Action[P, R]{def: def}
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, exists := registry[def.Name]; exists {
		panic("core: duplicate action " + def.Name)
	}
	registry[def.Name] = a
	return a
}

func Lookup(name string) (AnyAction, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	a, ok := registry[name]
	return a, ok
}
func Catalog() []AnyAction {
	registryMu.RLock()
	defer registryMu.RUnlock()
	a := slices.Collect(maps.Values(registry))
	slices.SortFunc(a, func(a, b AnyAction) int { return strings.Compare(a.Name(), b.Name()) })
	return a
}
func (a *Action[P, R]) Name() string        { return a.def.Name }
func (a *Action[P, R]) Description() string { return a.def.Description }
func (a *Action[P, R]) Mutating() bool      { return a.def.Mutating }
func (a *Action[P, R]) NewParams() any      { return new(P) }
func (a *Action[P, R]) Call(ctx context.Context, c *Core, p P) (R, error) {
	var zero R
	v, err := a.Invoke(ctx, c, &p)
	if err != nil {
		return zero, err
	}
	r, ok := v.(R)
	if !ok {
		return zero, fmt.Errorf("core: action %q returned %T", a.Name(), v)
	}
	return r, nil
}
func (a *Action[P, R]) Invoke(ctx context.Context, c *Core, raw any) (any, error) {
	p, ok := raw.(*P)
	if !ok {
		return nil, fmt.Errorf("core: action %q parameters have type %T", a.Name(), raw)
	}
	inner := func(ctx context.Context, inv Invocation) (any, error) {
		if v, ok := any(p).(interface{ Validate() error }); ok {
			if err := v.Validate(); err != nil {
				return nil, &ValidationError{Action: a.Name(), Err: err}
			}
		}
		return a.def.Handler(ctx, c, *p)
	}
	return c.run(ctx, Invocation{Action: a, Params: p}, inner)
}

func (c *Core) InvokeJSON(ctx context.Context, name string, data []byte) (any, error) {
	a, ok := Lookup(name)
	if !ok {
		return nil, ErrUnknownAction
	}
	p := a.NewParams()
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(p); err != nil {
		return nil, &ValidationError{Action: name, Err: err}
	}
	return a.Invoke(ctx, c, p)
}
