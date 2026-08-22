package server

import (
	"context"
	"log/slog"

	"github.com/jackc/logger4life/backend/core"
)

// auditMiddleware records every attempt to change state: which action, who
// asked, and whether it succeeded. Auditing belongs in middleware rather than
// in the actions because it is the one concern that must cover all of them —
// an action that forgot to log would be exactly the one worth having a record
// of.
//
// Reads are not recorded. They are the overwhelming majority of traffic and
// the request log already covers them; recording them here would bury the
// writes in noise. Parameters are not recorded either: they carry the values
// a user is logging, and an audit trail exists to say what happened rather
// than to become a second copy of the data.
func auditMiddleware(logger *slog.Logger) core.Middleware {
	return func(next core.Handler) core.Handler {
		return func(ctx context.Context, inv core.Invocation) (any, error) {
			if !inv.Action.Mutating() {
				return next(ctx, inv)
			}

			result, err := next(ctx, inv)

			attrs := []any{slog.String("action", inv.Action.Name())}
			if userID, ok := core.UserIDFromContext(ctx); ok {
				attrs = append(attrs, slog.String("user_id", userID))
			}
			if err != nil {
				// A refusal is worth as much as a success, and more when it
				// repeats, so it is recorded at the same level rather than
				// dropped as an ordinary error.
				logger.LogAttrs(ctx, slog.LevelInfo, "action refused", append(toAttrs(attrs), slog.String("error", err.Error()))...)
				return result, err
			}
			logger.LogAttrs(ctx, slog.LevelInfo, "action performed", toAttrs(attrs)...)
			return result, nil
		}
	}
}

func toAttrs(values []any) []slog.Attr {
	attrs := make([]slog.Attr, 0, len(values))
	for _, v := range values {
		if attr, ok := v.(slog.Attr); ok {
			attrs = append(attrs, attr)
		}
	}
	return attrs
}
