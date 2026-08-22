package server

import (
	"context"
	"errors"
	"net/http"

	"github.com/jackc/logger4life/backend/core"
)

type contextKey string

const userContextKey contextKey = "user"

type AuthUser = core.User

// loadSession keeps cookie parsing in HTTP, then delegates token validation
// and session persistence to the authenticate_session core action.
func loadSession(app *core.Core) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cookie, err := r.Cookie(sessionCookieName)
			if err != nil {
				next.ServeHTTP(w, r)
				return
			}

			user, err := core.AuthenticateSession.Call(r.Context(), app, core.AuthenticateSessionParams{Token: cookie.Value})
			if err != nil {
				if errors.Is(err, core.ErrInvalidSession) {
					clearSessionCookie(w)
				}
				next.ServeHTTP(w, r)
				return
			}

			ctx := core.WithUserID(r.Context(), user.ID)
			ctx = context.WithValue(ctx, userContextKey, &user)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if userFromContext(r.Context()) == nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func userFromContext(ctx context.Context) *AuthUser {
	user, _ := ctx.Value(userContextKey).(*AuthUser)
	return user
}
