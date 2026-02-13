package backend

import (
	"context"
	"encoding/hex"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
)

type contextKey string

const userContextKey contextKey = "user"

type AuthUser struct {
	ID       string
	Username string
	Email    *string
}

func loadSession(pool *pgxpool.Pool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cookie, err := r.Cookie(sessionCookieName)
			if err != nil {
				next.ServeHTTP(w, r)
				return
			}

			tokenBytes, err := hex.DecodeString(cookie.Value)
			if err != nil {
				clearSessionCookie(w)
				next.ServeHTTP(w, r)
				return
			}

			var user AuthUser
			err = pool.QueryRow(r.Context(),
				`SELECT u.id, u.username, u.email
				 FROM sessions s
				 JOIN users u ON s.user_id = u.id
				 WHERE s.token = $1 AND s.expires_at > now()`,
				tokenBytes,
			).Scan(&user.ID, &user.Username, &user.Email)

			if err != nil {
				clearSessionCookie(w)
				next.ServeHTTP(w, r)
				return
			}

			ctx := context.WithValue(r.Context(), userContextKey, &user)
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
