package backend

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type logAccess struct {
	LogID   string
	OwnerID string
	IsOwner bool
	Fields  []fieldDefinition
}

// checkLogAccess returns access info if the user owns the log or has shared access.
// Returns pgx.ErrNoRows if the log doesn't exist or user has no access.
func checkLogAccess(ctx context.Context, pool *pgxpool.Pool, logID, userID string) (*logAccess, error) {
	var ownerID string
	var fields []fieldDefinition
	err := pool.QueryRow(ctx,
		`SELECT user_id, fields FROM logs WHERE id = $1`,
		logID,
	).Scan(&ownerID, &fields)
	if err != nil {
		return nil, err
	}

	if ownerID == userID {
		return &logAccess{LogID: logID, OwnerID: ownerID, IsOwner: true, Fields: fields}, nil
	}

	var exists bool
	err = pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM log_shares WHERE log_id = $1 AND user_id = $2)`,
		logID, userID,
	).Scan(&exists)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, pgx.ErrNoRows
	}

	return &logAccess{LogID: logID, OwnerID: ownerID, IsOwner: false, Fields: fields}, nil
}

type shareTokenResponse struct {
	ShareToken string `json:"share_token"`
}

func handleCreateShareToken(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := userFromContext(r.Context())
		logID := chi.URLParam(r, "logID")

		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			return
		}

		tag, err := pool.Exec(r.Context(),
			`UPDATE logs SET share_token = $1 WHERE id = $2 AND user_id = $3`,
			b, logID, user.ID,
		)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			return
		}
		if tag.RowsAffected() == 0 {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "log not found"})
			return
		}

		writeJSON(w, http.StatusOK, shareTokenResponse{ShareToken: hex.EncodeToString(b)})
	}
}

func handleDeleteShareToken(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := userFromContext(r.Context())
		logID := chi.URLParam(r, "logID")

		tag, err := pool.Exec(r.Context(),
			`UPDATE logs SET share_token = NULL WHERE id = $1 AND user_id = $2`,
			logID, user.ID,
		)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			return
		}
		if tag.RowsAffected() == 0 {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "log not found"})
			return
		}

		w.WriteHeader(http.StatusNoContent)
	}
}

type sharedUserResponse struct {
	ID       string    `json:"id"`
	Username string    `json:"username"`
	SharedAt time.Time `json:"shared_at"`
}

func handleListShares(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := userFromContext(r.Context())
		logID := chi.URLParam(r, "logID")

		// Verify ownership
		var ownerID string
		err := pool.QueryRow(r.Context(),
			`SELECT user_id FROM logs WHERE id = $1`,
			logID,
		).Scan(&ownerID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "log not found"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			return
		}
		if ownerID != user.ID {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "log not found"})
			return
		}

		rows, err := pool.Query(r.Context(),
			`SELECT ls.id, u.username, ls.created_at
			 FROM log_shares ls
			 JOIN users u ON ls.user_id = u.id
			 WHERE ls.log_id = $1
			 ORDER BY ls.created_at`,
			logID,
		)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			return
		}
		defer rows.Close()

		shares := []sharedUserResponse{}
		for rows.Next() {
			var s sharedUserResponse
			if err := rows.Scan(&s.ID, &s.Username, &s.SharedAt); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
				return
			}
			shares = append(shares, s)
		}

		writeJSON(w, http.StatusOK, shares)
	}
}

func handleRemoveShare(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := userFromContext(r.Context())
		logID := chi.URLParam(r, "logID")
		shareID := chi.URLParam(r, "shareID")

		// Verify ownership
		var ownerID string
		err := pool.QueryRow(r.Context(),
			`SELECT user_id FROM logs WHERE id = $1`,
			logID,
		).Scan(&ownerID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "log not found"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			return
		}
		if ownerID != user.ID {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "log not found"})
			return
		}

		tag, err := pool.Exec(r.Context(),
			`DELETE FROM log_shares WHERE id = $1 AND log_id = $2`,
			shareID, logID,
		)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			return
		}
		if tag.RowsAffected() == 0 {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "share not found"})
			return
		}

		w.WriteHeader(http.StatusNoContent)
	}
}

type shareInfoResponse struct {
	LogID         string `json:"log_id"`
	LogName       string `json:"log_name"`
	OwnerUsername string `json:"owner_username"`
	IsOwner       bool   `json:"is_owner"`
	AlreadyMember bool   `json:"already_member"`
}

func handleGetShareInfo(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := userFromContext(r.Context())
		tokenHex := chi.URLParam(r, "token")

		tokenBytes, err := hex.DecodeString(tokenHex)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "invalid share link"})
			return
		}

		var logID, logName, ownerID, ownerUsername string
		err = pool.QueryRow(r.Context(),
			`SELECT l.id, l.name, l.user_id, u.username
			 FROM logs l
			 JOIN users u ON l.user_id = u.id
			 WHERE l.share_token = $1`,
			tokenBytes,
		).Scan(&logID, &logName, &ownerID, &ownerUsername)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "invalid share link"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			return
		}

		if ownerID == user.ID {
			writeJSON(w, http.StatusOK, shareInfoResponse{
				LogID:         logID,
				LogName:       logName,
				OwnerUsername: ownerUsername,
				IsOwner:       true,
			})
			return
		}

		var alreadyMember bool
		pool.QueryRow(r.Context(),
			`SELECT EXISTS(SELECT 1 FROM log_shares WHERE log_id = $1 AND user_id = $2)`,
			logID, user.ID,
		).Scan(&alreadyMember)

		writeJSON(w, http.StatusOK, shareInfoResponse{
			LogID:         logID,
			LogName:       logName,
			OwnerUsername: ownerUsername,
			AlreadyMember: alreadyMember,
		})
	}
}

type joinLogResponse struct {
	LogID   string `json:"log_id"`
	LogName string `json:"log_name"`
}

func handleJoinLog(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := userFromContext(r.Context())
		tokenHex := chi.URLParam(r, "token")

		tokenBytes, err := hex.DecodeString(tokenHex)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "invalid share link"})
			return
		}

		var logID, logName, ownerID string
		err = pool.QueryRow(r.Context(),
			`SELECT l.id, l.name, l.user_id
			 FROM logs l
			 WHERE l.share_token = $1`,
			tokenBytes,
		).Scan(&logID, &logName, &ownerID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "invalid share link"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			return
		}

		if ownerID == user.ID {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "you already own this log"})
			return
		}

		_, err = pool.Exec(r.Context(),
			`INSERT INTO log_shares (log_id, user_id) VALUES ($1, $2)`,
			logID, user.ID,
		)
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				// Already a member -- that's fine
				writeJSON(w, http.StatusOK, joinLogResponse{LogID: logID, LogName: logName})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			return
		}

		writeJSON(w, http.StatusCreated, joinLogResponse{LogID: logID, LogName: logName})
	}
}
