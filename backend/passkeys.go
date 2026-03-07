package backend

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/gofrs/uuid/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// webAuthnUser implements the webauthn.User interface.
type webAuthnUser struct {
	id          []byte
	name        string
	credentials []webauthn.Credential
}

func (u *webAuthnUser) WebAuthnID() []byte                         { return u.id }
func (u *webAuthnUser) WebAuthnName() string                       { return u.name }
func (u *webAuthnUser) WebAuthnDisplayName() string                { return u.name }
func (u *webAuthnUser) WebAuthnCredentials() []webauthn.Credential { return u.credentials }

func loadWebAuthnUser(ctx context.Context, pool *pgxpool.Pool, userID string) (*webAuthnUser, error) {
	uid, err := uuid.FromString(userID)
	if err != nil {
		return nil, err
	}

	var username string
	err = pool.QueryRow(ctx, `SELECT username FROM users WHERE id = $1`, uid).Scan(&username)
	if err != nil {
		return nil, err
	}

	rows, err := pool.Query(ctx,
		`SELECT credential_id, public_key, aaguid, sign_count, backup_eligible, backup_state FROM passkeys WHERE user_id = $1`,
		uid,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var credentials []webauthn.Credential
	for rows.Next() {
		var credID, pubKey, aaguid []byte
		var signCount int64
		var backupEligible, backupState bool
		if err := rows.Scan(&credID, &pubKey, &aaguid, &signCount, &backupEligible, &backupState); err != nil {
			return nil, err
		}
		credentials = append(credentials, webauthn.Credential{
			ID:        credID,
			PublicKey: pubKey,
			Flags: webauthn.CredentialFlags{
				BackupEligible: backupEligible,
				BackupState:    backupState,
			},
			Authenticator: webauthn.Authenticator{
				AAGUID:    aaguid,
				SignCount: uint32(signCount),
			},
		})
	}

	return &webAuthnUser{
		id:          uid.Bytes(),
		name:        username,
		credentials: credentials,
	}, nil
}

// Challenge storage helpers

func storeChallenge(ctx context.Context, pool *pgxpool.Pool, userID *string, session *webauthn.SessionData, challengeType string) (string, error) {
	// Clean up expired challenges opportunistically
	pool.Exec(ctx, `DELETE FROM webauthn_challenges WHERE expires_at < now()`)

	data, err := json.Marshal(session)
	if err != nil {
		return "", err
	}

	var id string
	err = pool.QueryRow(ctx,
		`INSERT INTO webauthn_challenges (user_id, session_data, type) VALUES ($1, $2, $3) RETURNING id`,
		userID, data, challengeType,
	).Scan(&id)
	return id, err
}

func loadAndDeleteChallenge(ctx context.Context, pool *pgxpool.Pool, challengeID string, challengeType string) (*webauthn.SessionData, error) {
	var data []byte
	var expiresAt time.Time
	err := pool.QueryRow(ctx,
		`DELETE FROM webauthn_challenges WHERE id = $1 AND type = $2 RETURNING session_data, expires_at`,
		challengeID, challengeType,
	).Scan(&data, &expiresAt)
	if err != nil {
		return nil, err
	}

	if time.Now().After(expiresAt) {
		return nil, pgx.ErrNoRows
	}

	var session webauthn.SessionData
	if err := json.Unmarshal(data, &session); err != nil {
		return nil, err
	}
	return &session, nil
}

// Registration handlers (authenticated)

func handlePasskeyRegisterBegin(pool *pgxpool.Pool, wan *webauthn.WebAuthn) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := userFromContext(r.Context())

		wanUser, err := loadWebAuthnUser(r.Context(), pool, user.ID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			return
		}

		creation, session, err := wan.BeginRegistration(wanUser,
			webauthn.WithResidentKeyRequirement(protocol.ResidentKeyRequirementPreferred),
			webauthn.WithConveyancePreference(protocol.PreferNoAttestation),
			webauthn.WithExclusions(webauthn.Credentials(wanUser.credentials).CredentialDescriptors()),
		)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			return
		}

		challengeID, err := storeChallenge(r.Context(), pool, &user.ID, session, "registration")
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			return
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"options":      creation,
			"challenge_id": challengeID,
		})
	}
}

func handlePasskeyRegisterFinish(pool *pgxpool.Pool, wan *webauthn.WebAuthn) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := userFromContext(r.Context())

		var req struct {
			ChallengeID string          `json:"challenge_id"`
			Credential  json.RawMessage `json:"credential"`
			Description string          `json:"description"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}

		req.Description = strings.TrimSpace(req.Description)
		if len(req.Description) > 100 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "description must be at most 100 characters"})
			return
		}

		session, err := loadAndDeleteChallenge(r.Context(), pool, req.ChallengeID, "registration")
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid or expired challenge"})
			return
		}

		wanUser, err := loadWebAuthnUser(r.Context(), pool, user.ID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			return
		}

		parsedResponse, err := protocol.ParseCredentialCreationResponseBody(bytes.NewReader(req.Credential))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid credential response"})
			return
		}

		credential, err := wan.CreateCredential(wanUser, *session, parsedResponse)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "credential verification failed"})
			return
		}

		var passkeyID string
		var createdAt time.Time
		err = pool.QueryRow(r.Context(),
			`INSERT INTO passkeys (user_id, credential_id, public_key, aaguid, sign_count, backup_eligible, backup_state, description)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			 RETURNING id, created_at`,
			user.ID, credential.ID, credential.PublicKey,
			credential.Authenticator.AAGUID, credential.Authenticator.SignCount,
			credential.Flags.BackupEligible, credential.Flags.BackupState,
			req.Description,
		).Scan(&passkeyID, &createdAt)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			return
		}

		writeJSON(w, http.StatusCreated, map[string]any{
			"id":          passkeyID,
			"description": req.Description,
			"created_at":  createdAt,
		})
	}
}

// Login handlers (public)

func handlePasskeyLoginBegin(pool *pgxpool.Pool, wan *webauthn.WebAuthn) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		assertion, session, err := wan.BeginDiscoverableLogin()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			return
		}

		challengeID, err := storeChallenge(r.Context(), pool, nil, session, "login")
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			return
		}

		writeJSON(w, http.StatusOK, map[string]any{
			"options":      assertion,
			"challenge_id": challengeID,
		})
	}
}

func handlePasskeyLoginFinish(pool *pgxpool.Pool, wan *webauthn.WebAuthn) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ChallengeID string          `json:"challenge_id"`
			Credential  json.RawMessage `json:"credential"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}

		session, err := loadAndDeleteChallenge(r.Context(), pool, req.ChallengeID, "login")
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid or expired challenge"})
			return
		}

		parsedResponse, err := protocol.ParseCredentialRequestResponseBody(bytes.NewReader(req.Credential))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid credential response"})
			return
		}

		handler := func(rawID, userHandle []byte) (webauthn.User, error) {
			uid, err := uuid.FromBytes(userHandle)
			if err != nil {
				return nil, err
			}
			return loadWebAuthnUser(r.Context(), pool, uid.String())
		}

		_, credential, err := wan.ValidatePasskeyLogin(handler, *session, parsedResponse)
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "passkey verification failed"})
			return
		}

		// Update sign count and backup state
		pool.Exec(r.Context(),
			`UPDATE passkeys SET sign_count = $1, backup_state = $2 WHERE credential_id = $3`,
			credential.Authenticator.SignCount, credential.Flags.BackupState, credential.ID,
		)

		// Look up the user ID from the passkey
		var userID string
		err = pool.QueryRow(r.Context(),
			`SELECT user_id FROM passkeys WHERE credential_id = $1`,
			credential.ID,
		).Scan(&userID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			return
		}

		token, err := createSession(r.Context(), pool, userID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			return
		}
		setSessionCookie(w, token)

		var resp userResponse
		err = pool.QueryRow(r.Context(),
			`SELECT id, username, email FROM users WHERE id = $1`,
			userID,
		).Scan(&resp.ID, &resp.Username, &resp.Email)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			return
		}

		writeJSON(w, http.StatusOK, resp)
	}
}

// Management handlers (authenticated)

type passkeyResponse struct {
	ID          string    `json:"id"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
}

func handleListPasskeys(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := userFromContext(r.Context())

		rows, err := pool.Query(r.Context(),
			`SELECT id, description, created_at FROM passkeys WHERE user_id = $1 ORDER BY created_at`,
			user.ID,
		)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			return
		}
		defer rows.Close()

		passkeys := []passkeyResponse{}
		for rows.Next() {
			var p passkeyResponse
			if err := rows.Scan(&p.ID, &p.Description, &p.CreatedAt); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
				return
			}
			passkeys = append(passkeys, p)
		}

		writeJSON(w, http.StatusOK, passkeys)
	}
}

func handleUpdatePasskey(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := userFromContext(r.Context())
		passkeyID := chi.URLParam(r, "passkeyID")

		var req struct {
			Description string `json:"description"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}

		req.Description = strings.TrimSpace(req.Description)
		if len(req.Description) > 100 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "description must be at most 100 characters"})
			return
		}

		var p passkeyResponse
		err := pool.QueryRow(r.Context(),
			`UPDATE passkeys SET description = $1 WHERE id = $2 AND user_id = $3
			 RETURNING id, description, created_at`,
			req.Description, passkeyID, user.ID,
		).Scan(&p.ID, &p.Description, &p.CreatedAt)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "passkey not found"})
			return
		}

		writeJSON(w, http.StatusOK, p)
	}
}

func handleDeletePasskey(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := userFromContext(r.Context())
		passkeyID := chi.URLParam(r, "passkeyID")

		result, err := pool.Exec(r.Context(),
			`DELETE FROM passkeys WHERE id = $1 AND user_id = $2`,
			passkeyID, user.ID,
		)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			return
		}
		if result.RowsAffected() == 0 {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "passkey not found"})
			return
		}

		writeJSON(w, http.StatusOK, map[string]string{"message": "passkey deleted"})
	}
}
