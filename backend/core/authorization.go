package core

import "context"

// RequireUser refuses any action not marked Public unless the context carries
// a caller. Every such action already establishes the caller for itself, so
// this adds no rule — what it adds is that the rule cannot be left out. An
// action written without the check is closed by default rather than open, and
// the omission shows up as a failing test rather than as an open endpoint.
//
// Because middleware wraps parameter validation rather than following it,
// this also fixes the order the two ran in: an anonymous caller is now turned
// away before being told anything about the parameters it sent.
func RequireUser() Middleware {
	return func(next Handler) Handler {
		return func(ctx context.Context, inv Invocation) (any, error) {
			if !inv.Action.Public() {
				if _, ok := UserIDFromContext(ctx); !ok {
					return nil, ErrUnauthenticated
				}
			}
			return next(ctx, inv)
		}
	}
}
