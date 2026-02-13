package backend

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

const sessionCookieName = "session_token"
const sessionDuration = 30 * 24 * time.Hour

type registerRequest struct {
	Username string  `json:"username"`
	Email    *string `json:"email,omitempty"`
	Password string  `json:"password"`
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type userResponse struct {
	ID       string  `json:"id"`
	Username string  `json:"username"`
	Email    *string `json:"email,omitempty"`
}

func handleHello(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var msg string
		err := pool.QueryRow(r.Context(), "select 'Hello, World!'").Scan(&msg)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"message": msg})
	}
}

func handleRegister(pool *pgxpool.Pool, allowRegistration bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !allowRegistration {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "registration is currently disabled"})
			return
		}

		var req registerRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}

		req.Username = strings.TrimSpace(req.Username)
		if len(req.Username) == 0 || len(req.Username) > 30 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "username must be 1-30 characters"})
			return
		}
		if len(req.Password) < 8 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "password must be at least 8 characters"})
			return
		}
		if req.Email != nil {
			trimmed := strings.TrimSpace(*req.Email)
			if trimmed == "" {
				req.Email = nil
			} else {
				req.Email = &trimmed
			}
		}

		hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			return
		}

		var user userResponse
		err = pool.QueryRow(r.Context(),
			`INSERT INTO users (username, email, password_hash)
			 VALUES ($1, $2, $3)
			 RETURNING id, username, email`,
			req.Username, req.Email, string(hash),
		).Scan(&user.ID, &user.Username, &user.Email)

		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				writeJSON(w, http.StatusConflict, map[string]string{"error": "username already taken"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			return
		}

		token, err := createSession(r.Context(), pool, user.ID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			return
		}
		setSessionCookie(w, token)
		writeJSON(w, http.StatusCreated, user)
	}
}

func handleLogin(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req loginRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}

		var id, username, passwordHash string
		var email *string
		err := pool.QueryRow(r.Context(),
			`SELECT id, username, email, password_hash FROM users WHERE lower(username) = lower($1)`,
			req.Username,
		).Scan(&id, &username, &email, &passwordHash)

		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid username or password"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			return
		}

		if err := bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(req.Password)); err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid username or password"})
			return
		}

		token, err := createSession(r.Context(), pool, id)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			return
		}
		setSessionCookie(w, token)
		writeJSON(w, http.StatusOK, userResponse{ID: id, Username: username, Email: email})
	}
}

func handleLogout(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookieName)
		if err == nil {
			tokenBytes, err := hex.DecodeString(cookie.Value)
			if err == nil {
				pool.Exec(r.Context(), `DELETE FROM sessions WHERE token = $1`, tokenBytes)
			}
		}
		clearSessionCookie(w)
		writeJSON(w, http.StatusOK, map[string]string{"message": "logged out"})
	}
}

func handleMe(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		return
	}
	writeJSON(w, http.StatusOK, userResponse{ID: user.ID, Username: user.Username, Email: user.Email})
}

// createSession generates a random token, stores it as raw bytes in the
// sessions table, and returns the hex-encoded token for cookie use.
func createSession(ctx context.Context, pool *pgxpool.Pool, userID string) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	_, err := pool.Exec(ctx,
		`INSERT INTO sessions (user_id, token) VALUES ($1, $2)`,
		userID, b,
	)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func setSessionCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(sessionDuration.Seconds()),
	})
}

func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
