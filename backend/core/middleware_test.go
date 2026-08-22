package core

import (
	"context"
	"errors"
	"slices"
	"testing"
)

// Middleware is the only place a cross-cutting rule — authorization, audit,
// tracing — can sit in front of every action at once. These tests pin the
// contract such a rule is written against: what a middleware can see, what it
// can change, and where it sits relative to parameter validation.

type middlewareMarkerKey struct{}

type middlewareParams struct {
	Value string `json:"value"`
}

func (p *middlewareParams) Validate() error {
	if p.Value == "reject" {
		return errors.New("value is rejected")
	}
	return nil
}

// middlewareHandlerCalls counts handler entries so a test can prove the
// handler did not run. Tests in this package are sequential; each test that
// reads it resets it first.
var middlewareHandlerCalls int

var errMiddlewareHandler = errors.New("handler failed")

var middlewareTestAction = Define(ActionDef[middlewareParams, string]{
	Name: "core_test_middleware", Description: "middleware test action", Mutating: true,
	Handler: func(ctx context.Context, _ *Core, p middlewareParams) (string, error) {
		middlewareHandlerCalls++
		if p.Value == "boom" {
			return "", errMiddlewareHandler
		}
		marker, _ := ctx.Value(middlewareMarkerKey{}).(string)
		return p.Value + marker, nil
	},
})

func invokeMiddlewareAction(c *Core, value string) (any, error) {
	return c.InvokeJSON(context.Background(), middlewareTestAction.Name(), []byte(`{"value":"`+value+`"}`))
}

// Both invocation paths run the same stack, and the first entry in
// Config.Middleware is the outermost wrapper — the order a reader expects from
// an HTTP middleware stack, not the reverse order run applies them in.
func TestMiddlewareRunsInDeclaredOrderOnBothInvocationPaths(t *testing.T) {
	paths := []struct {
		name string
		call func(*Core) (any, error)
	}{
		{"typed", func(c *Core) (any, error) {
			return middlewareTestAction.Call(context.Background(), c, middlewareParams{Value: "ok"})
		}},
		{"json", func(c *Core) (any, error) { return invokeMiddlewareAction(c, "ok") }},
	}
	for _, path := range paths {
		t.Run(path.name, func(t *testing.T) {
			var trace []string
			record := func(name string) Middleware {
				return func(next Handler) Handler {
					return func(ctx context.Context, inv Invocation) (any, error) {
						trace = append(trace, "enter "+name)
						v, err := next(ctx, inv)
						trace = append(trace, "exit "+name)
						return v, err
					}
				}
			}
			c := New(Config{Middleware: []Middleware{record("outer"), record("inner")}})

			got, err := path.call(c)
			if err != nil || got != "ok" {
				t.Fatalf("invocation = %v, %v", got, err)
			}
			want := []string{"enter outer", "enter inner", "exit inner", "exit outer"}
			if !slices.Equal(trace, want) {
				t.Errorf("trace = %v, want %v", trace, want)
			}
		})
	}
}

// The middleware stack is worth nothing if an action can slip past it. A
// denying middleware short-circuits before the handler, so this drives every
// action in the catalog through a Core with no ports wired at all.
func TestEveryCatalogActionPassesThroughMiddleware(t *testing.T) {
	denied := errors.New("denied by policy")
	var seen []string
	c := New(Config{Middleware: []Middleware{func(Handler) Handler {
		return func(_ context.Context, inv Invocation) (any, error) {
			seen = append(seen, inv.Action.Name())
			return nil, denied
		}
	}}})

	catalog := Catalog()
	for _, action := range catalog {
		if _, err := action.Invoke(context.Background(), c, action.NewParams()); !errors.Is(err, denied) {
			t.Errorf("action %q returned %v, want the middleware's denial", action.Name(), err)
		}
	}
	if len(seen) != len(catalog) {
		t.Errorf("middleware saw %d actions, want all %d in the catalog", len(seen), len(catalog))
	}
}

// Policy is written against the invocation, so a middleware must be able to
// identify the action — including whether it changes state, which is what an
// audit trail records — and read its parameters before the handler runs.
func TestMiddlewareObservesActionIdentityAndParameters(t *testing.T) {
	var seen Invocation
	c := New(Config{Middleware: []Middleware{func(next Handler) Handler {
		return func(ctx context.Context, inv Invocation) (any, error) {
			seen = inv
			return next(ctx, inv)
		}
	}}})

	if _, err := invokeMiddlewareAction(c, "audited"); err != nil {
		t.Fatal(err)
	}
	if seen.Action == nil || seen.Action.Name() != middlewareTestAction.Name() || !seen.Action.Mutating() {
		t.Fatalf("observed action = %#v", seen.Action)
	}
	params, ok := seen.Params.(*middlewareParams)
	if !ok || params.Value != "audited" {
		t.Fatalf("observed params = %#v", seen.Params)
	}
}

// Authentication middleware exists to put identity into the context, so the
// context a middleware passes down must be the one the handler receives.
func TestMiddlewareContextReachesTheHandler(t *testing.T) {
	c := New(Config{Middleware: []Middleware{func(next Handler) Handler {
		return func(ctx context.Context, inv Invocation) (any, error) {
			return next(context.WithValue(ctx, middlewareMarkerKey{}, "+marked"), inv)
		}
	}}})

	got, err := middlewareTestAction.Call(context.Background(), c, middlewareParams{Value: "ok"})
	if err != nil || got != "ok+marked" {
		t.Fatalf("Call() = %q, %v", got, err)
	}
}

// Parameter validation runs inside the stack, not ahead of it. A middleware
// therefore sees calls that go on to fail validation: an audit trail records
// the attempt, and an authorization check can refuse a call whose parameters
// were never valid to begin with.
func TestMiddlewareWrapsParameterValidation(t *testing.T) {
	entries := 0
	var observed error
	c := New(Config{Middleware: []Middleware{func(next Handler) Handler {
		return func(ctx context.Context, inv Invocation) (any, error) {
			entries++
			v, err := next(ctx, inv)
			observed = err
			return v, err
		}
	}}})

	middlewareHandlerCalls = 0
	var validationErr *ValidationError
	if _, err := middlewareTestAction.Call(context.Background(), c, middlewareParams{Value: "reject"}); !errors.As(err, &validationErr) {
		t.Fatalf("Call() error = %T %v, want ValidationError", err, err)
	}
	if entries != 1 {
		t.Errorf("middleware ran %d times for an invalid call, want 1", entries)
	}
	if !errors.As(observed, &validationErr) {
		t.Errorf("middleware observed %v, want the validation error", observed)
	}
	if middlewareHandlerCalls != 0 {
		t.Errorf("handler ran %d times despite invalid parameters", middlewareHandlerCalls)
	}
}

// A middleware that refuses must stop the action outright, and the caller must
// see the middleware's own error rather than a zero value and no error.
func TestMiddlewareCanDenyBeforeTheHandlerRuns(t *testing.T) {
	denied := errors.New("denied by policy")
	c := New(Config{Middleware: []Middleware{func(Handler) Handler {
		return func(context.Context, Invocation) (any, error) { return nil, denied }
	}}})

	middlewareHandlerCalls = 0
	got, err := middlewareTestAction.Call(context.Background(), c, middlewareParams{Value: "ok"})
	if !errors.Is(err, denied) {
		t.Fatalf("Call() error = %v, want the middleware's denial", err)
	}
	if got != "" {
		t.Errorf("Call() returned %q alongside a denial", got)
	}
	if middlewareHandlerCalls != 0 {
		t.Errorf("handler ran %d times behind a denying middleware", middlewareHandlerCalls)
	}
}

// A handler's own failure passes back out through the stack unchanged, so a
// middleware can log or translate it without the core having buried it first.
func TestMiddlewareObservesHandlerFailures(t *testing.T) {
	var observed error
	c := New(Config{Middleware: []Middleware{func(next Handler) Handler {
		return func(ctx context.Context, inv Invocation) (any, error) {
			v, err := next(ctx, inv)
			observed = err
			return v, err
		}
	}}})

	if _, err := middlewareTestAction.Call(context.Background(), c, middlewareParams{Value: "boom"}); !errors.Is(err, errMiddlewareHandler) {
		t.Fatalf("Call() error = %v, want the handler's error", err)
	}
	if !errors.Is(observed, errMiddlewareHandler) {
		t.Errorf("middleware observed %v, want the handler's error", observed)
	}
}

// The stack is untyped in the middle: a middleware that substitutes a result of
// a different type — a cache returning nothing on a hit it cannot rebuild, a
// policy layer blanking a response — passes through the dynamic path but fails
// the typed one, because Call asserts the action's declared result type. A
// middleware that wants to suppress a result has to return an error too.
func TestMiddlewareSubstitutingAResultTypeFailsTypedCalls(t *testing.T) {
	c := New(Config{Middleware: []Middleware{func(Handler) Handler {
		return func(context.Context, Invocation) (any, error) { return nil, nil }
	}}})

	if _, err := middlewareTestAction.Call(context.Background(), c, middlewareParams{Value: "ok"}); err == nil {
		t.Error("typed Call accepted a result of the wrong type")
	}
	got, err := invokeMiddlewareAction(c, "ok")
	if err != nil || got != nil {
		t.Errorf("InvokeJSON() = %v, %v; the dynamic path does not check result types", got, err)
	}
}

// InvokeJSON resolves the action and decodes parameters before entering the
// stack, so a middleware never sees an unknown action or a malformed body. An
// audit middleware records what ran, not what was turned away at the door;
// rejected calls have to be logged by the adapter that received them.
func TestMiddlewareDoesNotSeeCallsRejectedBeforeDispatch(t *testing.T) {
	entries := 0
	c := New(Config{Middleware: []Middleware{func(next Handler) Handler {
		return func(ctx context.Context, inv Invocation) (any, error) {
			entries++
			return next(ctx, inv)
		}
	}}})

	if _, err := c.InvokeJSON(context.Background(), "core_test_no_such_action", []byte(`{}`)); !errors.Is(err, ErrUnknownAction) {
		t.Fatalf("unknown action error = %v, want ErrUnknownAction", err)
	}
	var validationErr *ValidationError
	if _, err := c.InvokeJSON(context.Background(), middlewareTestAction.Name(), []byte(`{"unexpected":1}`)); !errors.As(err, &validationErr) {
		t.Fatalf("undecodable params error = %T %v, want ValidationError", err, err)
	}
	if entries != 0 {
		t.Errorf("middleware ran %d times for calls rejected before dispatch", entries)
	}
}
