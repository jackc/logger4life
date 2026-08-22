package backend

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/httplog/v3"
	"github.com/jackc/logger4life/backend/core"
	"github.com/jackc/pgx/v5/pgxpool"
)

const sessionCookieName = "session_token"

func handleHello(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var msg string
		if err := pool.QueryRow(r.Context(), "select 'Hello, World!'").Scan(&msg); err != nil {
			internalError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"message": msg})
	}
}

func writeAuthError(w http.ResponseWriter, r *http.Request, err error) {
	var validationErr *core.ValidationError
	switch {
	case errors.As(err, &validationErr):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": validationErr.Err.Error()})
	case errors.Is(err, core.ErrUsernameTaken):
		writeJSON(w, http.StatusConflict, map[string]string{"error": core.ErrUsernameTaken.Error()})
	case errors.Is(err, core.ErrEmailTaken):
		writeJSON(w, http.StatusConflict, map[string]string{"error": core.ErrEmailTaken.Error()})
	case errors.Is(err, core.ErrInvalidCredentials):
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": core.ErrInvalidCredentials.Error()})
	case errors.Is(err, core.ErrIncorrectPassword):
		writeJSON(w, http.StatusForbidden, map[string]string{"error": core.ErrIncorrectPassword.Error()})
	case errors.Is(err, core.ErrUnauthenticated), errors.Is(err, core.ErrInvalidSession):
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
	default:
		internalError(w, r, err)
	}
}

func handleRegister(app *core.Core, allowRegistration bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !allowRegistration {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "registration is currently disabled"})
			return
		}
		var params core.RegisterUserParams
		if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}
		session, err := core.RegisterUser.Call(r.Context(), app, params)
		if err != nil {
			writeAuthError(w, r, err)
			return
		}
		setSessionCookie(w, session.Token, session.ExpiresAt)
		writeJSON(w, http.StatusCreated, session.User)
	}
}

func handleLogin(app *core.Core) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var params core.LoginWithPasswordParams
		if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}
		session, err := core.LoginWithPassword.Call(r.Context(), app, params)
		if err != nil {
			writeAuthError(w, r, err)
			return
		}
		setSessionCookie(w, session.Token, session.ExpiresAt)
		writeJSON(w, http.StatusOK, session.User)
	}
}

func handleLogout(app *core.Core) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if cookie, err := r.Cookie(sessionCookieName); err == nil {
			if _, err := core.Logout.Call(r.Context(), app, core.LogoutParams{Token: cookie.Value}); err != nil {
				writeAuthError(w, r, err)
				return
			}
		}
		clearSessionCookie(w)
		writeJSON(w, http.StatusOK, map[string]string{"message": "logged out"})
	}
}

func handleMe(app *core.Core) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, err := core.GetProfile.Call(actionContext(r), app, core.GetProfileParams{})
		if err != nil {
			writeAuthError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, user)
	}
}

// secureCookies controls the Cookie.Secure attribute. It is set once during
// server startup and only read afterward.
var secureCookies bool

func setSessionCookie(w http.ResponseWriter, token string, expiresAt ...time.Time) {
	maxAge := int(core.SessionDuration.Seconds())
	cookie := &http.Cookie{
		Name: sessionCookieName, Value: token, Path: "/", HttpOnly: true,
		Secure: secureCookies, SameSite: http.SameSiteLaxMode, MaxAge: maxAge,
	}
	if len(expiresAt) > 0 {
		cookie.Expires = expiresAt[0]
		maxAge = int(time.Until(expiresAt[0]).Seconds())
		if maxAge > 0 {
			cookie.MaxAge = maxAge
		}
	}
	http.SetCookie(w, cookie)
}

func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookieName, Value: "", Path: "/", HttpOnly: true,
		Secure: secureCookies, SameSite: http.SameSiteLaxMode, MaxAge: -1,
	})
}

func handleChangeEmail(app *core.Core) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var params core.ChangeEmailParams
		if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}
		user, err := core.ChangeEmail.Call(actionContext(r), app, params)
		if err != nil {
			writeAuthError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, user)
	}
}

func handleChangePassword(app *core.Core) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var params core.ChangePasswordParams
		if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}
		if _, err := core.ChangePassword.Call(actionContext(r), app, params); err != nil {
			writeAuthError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"message": "password updated"})
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func internalError(w http.ResponseWriter, r *http.Request, err error) {
	httplog.SetError(r.Context(), err)
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
}
