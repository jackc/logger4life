package server

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/logger4life/backend/core"
)

func writePasskeyError(w http.ResponseWriter, r *http.Request, err error) {
	var validationErr *core.ValidationError
	switch {
	case errors.As(err, &validationErr):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": validationErr.Err.Error()})
	case errors.Is(err, core.ErrPasskeyNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": core.ErrPasskeyNotFound.Error()})
	case errors.Is(err, core.ErrPasskeyAlreadyRegistered):
		writeJSON(w, http.StatusConflict, map[string]string{"error": core.ErrPasskeyAlreadyRegistered.Error()})
	case errors.Is(err, core.ErrInvalidPasskeyChallenge), errors.Is(err, core.ErrPasskeyVerificationFailed):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	case errors.Is(err, core.ErrUnauthenticated):
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
	case errors.Is(err, core.ErrPasskeysDisabled):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
	default:
		internalError(w, r, err)
	}
}

// Registration handlers translate HTTP JSON and trusted authentication
// context into passkey catalog actions. WebAuthn verification and persistence
// orchestration remain inside core.
func handlePasskeyRegisterBegin(app *core.Core) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		result, err := core.BeginPasskeyRegistration.Call(actionContext(r), app, core.BeginPasskeyRegistrationParams{})
		if err != nil {
			writePasskeyError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func handlePasskeyRegisterFinish(app *core.Core) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var params core.FinishPasskeyRegistrationParams
		if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}
		result, err := core.FinishPasskeyRegistration.Call(actionContext(r), app, params)
		if err != nil {
			writePasskeyError(w, r, err)
			return
		}
		writeJSON(w, http.StatusCreated, result)
	}
}

// Login handlers are public. A successful finish action returns the session
// material needed by this adapter to set its transport-specific cookie.
func handlePasskeyLoginBegin(app *core.Core) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		result, err := core.BeginPasskeyLogin.Call(r.Context(), app, core.BeginPasskeyLoginParams{})
		if err != nil {
			writePasskeyError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	}
}

func handlePasskeyLoginFinish(app *core.Core) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var params core.FinishPasskeyLoginParams
		if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}
		session, err := core.FinishPasskeyLogin.Call(r.Context(), app, params)
		if err != nil {
			if errors.Is(err, core.ErrInvalidPasskeyChallenge) || errors.Is(err, core.ErrPasskeyVerificationFailed) {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
				return
			}
			writePasskeyError(w, r, err)
			return
		}
		setSessionCookie(w, session.Token, session.ExpiresAt)
		writeJSON(w, http.StatusOK, session.User)
	}
}

func handleListPasskeys(app *core.Core) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		passkeys, err := core.ListPasskeys.Call(actionContext(r), app, core.ListPasskeysParams{})
		if err != nil {
			writePasskeyError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, passkeys)
	}
}

func handleUpdatePasskey(app *core.Core) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Description string `json:"description"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}
		passkey, err := core.RenamePasskey.Call(actionContext(r), app, core.RenamePasskeyParams{
			PasskeyID: chi.URLParam(r, "passkeyID"), Description: body.Description,
		})
		if err != nil {
			writePasskeyError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, passkey)
	}
}

func handleDeletePasskey(app *core.Core) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, err := core.DeletePasskey.Call(actionContext(r), app, core.DeletePasskeyParams{
			PasskeyID: chi.URLParam(r, "passkeyID"),
		})
		if err != nil {
			writePasskeyError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"message": "passkey deleted"})
	}
}
