package backend

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/jackc/logger4life/backend/core"
	"github.com/jackc/logger4life/backend/pgstore"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestRouter(t *testing.T) *httptest.Server {
	return setupTestRouterWithConfig(t, true)
}

func setupTestRouterWithConfig(t *testing.T, allowRegistration bool) *httptest.Server {
	t.Helper()

	pool, err := pgxpool.New(context.Background(), "postgres://postgres:postgres@localhost:5432/logger4life_test")
	require.NoError(t, err)

	t.Cleanup(func() {
		pool.Exec(context.Background(), "DELETE FROM webauthn_challenges")
		pool.Exec(context.Background(), "DELETE FROM passkeys")
		pool.Exec(context.Background(), "DELETE FROM saved_sql_queries")
		pool.Exec(context.Background(), "DELETE FROM user_log_placements")
		pool.Exec(context.Background(), "DELETE FROM folders")
		pool.Exec(context.Background(), "DELETE FROM log_shares")
		pool.Exec(context.Background(), "DELETE FROM log_entries")
		pool.Exec(context.Background(), "DELETE FROM logs")
		pool.Exec(context.Background(), "DELETE FROM sessions")
		pool.Exec(context.Background(), "DELETE FROM users")
		pool.Close()
	})

	wan, err := webauthn.New(&webauthn.Config{
		RPDisplayName: "Logger4Life Test",
		RPID:          "localhost",
		RPOrigins:     []string{"http://localhost"},
	})
	require.NoError(t, err)
	store := pgstore.New(pool)
	app := core.New(core.Config{Users: store, Sessions: store, Passkeys: store, Challenges: store, WebAuthn: wan, Tx: store, UserSQL: store})

	r := chi.NewRouter()
	r.Use(loadSession(app))
	r.Get("/api/settings", handleSettings(Config{AllowRegistration: allowRegistration, WebAuthnRPID: "localhost", WebAuthnOrigin: "http://localhost"}))
	r.Post("/api/register", handleRegister(pool, allowRegistration, app))
	r.Post("/api/login", handleLogin(pool, app))
	r.Post("/api/passkey-login/begin", handlePasskeyLoginBegin(app))
	r.Post("/api/passkey-login/finish", handlePasskeyLoginFinish(app))
	r.Group(func(r chi.Router) {
		r.Use(requireAuth)
		r.Post("/api/logout", handleLogout(pool, app))
		r.Get("/api/me", handleMe(app))
		r.Put("/api/me/email", handleChangeEmail(pool, app))
		r.Put("/api/me/password", handleChangePassword(pool, app))
		r.Get("/api/me/passkeys", handleListPasskeys(app))
		r.Put("/api/me/passkeys/{passkeyID}", handleUpdatePasskey(app))
		r.Delete("/api/me/passkeys/{passkeyID}", handleDeletePasskey(app))
		r.Post("/api/me/passkeys/register/begin", handlePasskeyRegisterBegin(app))
		r.Post("/api/me/passkeys/register/finish", handlePasskeyRegisterFinish(app))
		r.Post("/api/logs", handleCreateLog(pool))
		r.Get("/api/logs", handleListLogs(pool))
		r.Get("/api/logs/{logID}", handleGetLog(pool))
		r.Put("/api/logs/{logID}", handleUpdateLog(pool))
		r.Delete("/api/logs/{logID}", handleDeleteLog(pool))
		r.Put("/api/logs/{logID}/placement", handleUpdateLogPlacement(pool))
		r.Put("/api/logs/{logID}/pin", handlePinLog(pool))
		r.Put("/api/logs/{logID}/home-position", handleUpdateLogHomePosition(pool))
		r.Post("/api/folders", handleCreateFolder(pool))
		r.Get("/api/folders", handleListFolders(pool))
		r.Put("/api/folders/{folderID}", handleRenameFolder(pool))
		r.Put("/api/folders/{folderID}/move", handleMoveFolder(pool))
		r.Delete("/api/folders/{folderID}", handleDeleteFolder(pool))
		r.Post("/api/logs/{logID}/entries", handleCreateLogEntry(pool))
		r.Get("/api/logs/{logID}/entries", handleListLogEntries(pool))
		r.Put("/api/logs/{logID}/entries/{entryID}", handleUpdateLogEntry(pool))
		r.Delete("/api/logs/{logID}/entries/{entryID}", handleDeleteLogEntry(pool))
		r.Post("/api/logs/{logID}/share-token", handleCreateShareToken(pool))
		r.Delete("/api/logs/{logID}/share-token", handleDeleteShareToken(pool))
		r.Get("/api/logs/{logID}/shares", handleListShares(pool))
		r.Delete("/api/logs/{logID}/shares/{shareID}", handleRemoveShare(pool))
		r.Get("/api/join/{token}", handleGetShareInfo(pool))
		r.Post("/api/join/{token}", handleJoinLog(pool))

		r.Post("/api/sql/execute", handleExecuteSQL(app))
		r.Get("/api/sql/schema", handleGetSQLSchema(pool))
		r.Get("/api/sql/saved", handleListSavedQueries(pool))
		r.Post("/api/sql/saved", handleCreateSavedQuery(pool))
		r.Put("/api/sql/saved/{id}", handleUpdateSavedQuery(pool))
		r.Delete("/api/sql/saved/{id}", handleDeleteSavedQuery(pool))
	})

	return httptest.NewServer(r)
}

func postJSON(url string, body any, cookies []*http.Cookie) (*http.Response, map[string]any) {
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", url, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	for _, c := range cookies {
		req.AddCookie(c)
	}
	client := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, _ := client.Do(req)
	var result map[string]any
	json.NewDecoder(resp.Body).Decode(&result)
	resp.Body.Close()
	return resp, result
}

func putJSON(url string, body any, cookies []*http.Cookie) (*http.Response, map[string]any) {
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest("PUT", url, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	for _, c := range cookies {
		req.AddCookie(c)
	}
	client := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, _ := client.Do(req)
	var result map[string]any
	json.NewDecoder(resp.Body).Decode(&result)
	resp.Body.Close()
	return resp, result
}

func deleteJSON(url string, cookies []*http.Cookie) (*http.Response, map[string]any) {
	req, _ := http.NewRequest("DELETE", url, nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	client := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, _ := client.Do(req)
	var result map[string]any
	json.NewDecoder(resp.Body).Decode(&result)
	resp.Body.Close()
	return resp, result
}

func getJSON(url string, cookies []*http.Cookie) (*http.Response, map[string]any) {
	req, _ := http.NewRequest("GET", url, nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	client := &http.Client{}
	resp, _ := client.Do(req)
	var result map[string]any
	json.NewDecoder(resp.Body).Decode(&result)
	resp.Body.Close()
	return resp, result
}

func findSessionCookie(resp *http.Response) *http.Cookie {
	for _, c := range resp.Cookies() {
		if c.Name == sessionCookieName {
			return c
		}
	}
	return nil
}

func TestRegister_Success(t *testing.T) {
	srv := setupTestRouter(t)
	defer srv.Close()

	resp, body := postJSON(srv.URL+"/api/register", map[string]any{
		"username": "alice",
		"email":    "alice@example.com",
		"password": "password123",
	}, nil)

	assert.Equal(t, http.StatusCreated, resp.StatusCode)
	assert.Equal(t, "alice", body["username"])
	assert.Equal(t, "alice@example.com", body["email"])
	assert.NotEmpty(t, body["id"])

	cookie := findSessionCookie(resp)
	require.NotNil(t, cookie)
	assert.NotEmpty(t, cookie.Value)
}

func TestRegister_NoEmail(t *testing.T) {
	srv := setupTestRouter(t)
	defer srv.Close()

	resp, body := postJSON(srv.URL+"/api/register", map[string]any{
		"username": "bob",
		"password": "password123",
	}, nil)

	assert.Equal(t, http.StatusCreated, resp.StatusCode)
	assert.Equal(t, "bob", body["username"])
	assert.Nil(t, body["email"])
}

func TestRegister_DuplicateUsername(t *testing.T) {
	srv := setupTestRouter(t)
	defer srv.Close()

	postJSON(srv.URL+"/api/register", map[string]any{
		"username": "alice",
		"password": "password123",
	}, nil)

	resp, body := postJSON(srv.URL+"/api/register", map[string]any{
		"username": "alice",
		"password": "password456",
	}, nil)

	assert.Equal(t, http.StatusConflict, resp.StatusCode)
	assert.Equal(t, "username already taken", body["error"])
}

func TestRegister_MissingUsername(t *testing.T) {
	srv := setupTestRouter(t)
	defer srv.Close()

	resp, body := postJSON(srv.URL+"/api/register", map[string]any{
		"password": "password123",
	}, nil)

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Contains(t, body["error"], "username")
}

func TestRegister_ShortPassword(t *testing.T) {
	srv := setupTestRouter(t)
	defer srv.Close()

	resp, body := postJSON(srv.URL+"/api/register", map[string]any{
		"username": "alice",
		"password": "short",
	}, nil)

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Contains(t, body["error"], "password")
}

func TestLogin_Success(t *testing.T) {
	srv := setupTestRouter(t)
	defer srv.Close()

	postJSON(srv.URL+"/api/register", map[string]any{
		"username": "alice",
		"password": "password123",
	}, nil)

	resp, body := postJSON(srv.URL+"/api/login", map[string]any{
		"username": "alice",
		"password": "password123",
	}, nil)

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "alice", body["username"])

	cookie := findSessionCookie(resp)
	require.NotNil(t, cookie)
	assert.NotEmpty(t, cookie.Value)
}

func TestLogin_CaseInsensitiveUsername(t *testing.T) {
	srv := setupTestRouter(t)
	defer srv.Close()

	postJSON(srv.URL+"/api/register", map[string]any{
		"username": "Alice",
		"password": "password123",
	}, nil)

	resp, body := postJSON(srv.URL+"/api/login", map[string]any{
		"username": "alice",
		"password": "password123",
	}, nil)

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "Alice", body["username"])
}

func TestLogin_WrongPassword(t *testing.T) {
	srv := setupTestRouter(t)
	defer srv.Close()

	postJSON(srv.URL+"/api/register", map[string]any{
		"username": "alice",
		"password": "password123",
	}, nil)

	resp, body := postJSON(srv.URL+"/api/login", map[string]any{
		"username": "alice",
		"password": "wrongpassword",
	}, nil)

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, "invalid username or password", body["error"])
}

func TestLogin_NonexistentUser(t *testing.T) {
	srv := setupTestRouter(t)
	defer srv.Close()

	resp, body := postJSON(srv.URL+"/api/login", map[string]any{
		"username": "nobody",
		"password": "password123",
	}, nil)

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, "invalid username or password", body["error"])
}

func TestMe_Authenticated(t *testing.T) {
	srv := setupTestRouter(t)
	defer srv.Close()

	regResp, _ := postJSON(srv.URL+"/api/register", map[string]any{
		"username": "alice",
		"email":    "alice@example.com",
		"password": "password123",
	}, nil)

	cookie := findSessionCookie(regResp)
	require.NotNil(t, cookie)

	resp, body := getJSON(srv.URL+"/api/me", []*http.Cookie{cookie})

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "alice", body["username"])
	assert.Equal(t, "alice@example.com", body["email"])
}

func TestMe_Unauthenticated(t *testing.T) {
	srv := setupTestRouter(t)
	defer srv.Close()

	resp, body := getJSON(srv.URL+"/api/me", nil)

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, "authentication required", body["error"])
}

func TestLogout(t *testing.T) {
	srv := setupTestRouter(t)
	defer srv.Close()

	regResp, _ := postJSON(srv.URL+"/api/register", map[string]any{
		"username": "alice",
		"password": "password123",
	}, nil)

	cookie := findSessionCookie(regResp)
	require.NotNil(t, cookie)

	// Logout
	logoutResp, _ := postJSON(srv.URL+"/api/logout", map[string]any{}, []*http.Cookie{cookie})
	assert.Equal(t, http.StatusOK, logoutResp.StatusCode)

	// Session should be invalidated
	resp, body := getJSON(srv.URL+"/api/me", []*http.Cookie{cookie})
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, "authentication required", body["error"])
}

func TestRegister_Disabled(t *testing.T) {
	srv := setupTestRouterWithConfig(t, false)
	defer srv.Close()

	resp, body := postJSON(srv.URL+"/api/register", map[string]any{
		"username": "alice",
		"password": "password123",
	}, nil)

	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	assert.Equal(t, "registration is currently disabled", body["error"])
}

func TestSettings_RegistrationEnabled(t *testing.T) {
	srv := setupTestRouterWithConfig(t, true)
	defer srv.Close()

	resp, body := getJSON(srv.URL+"/api/settings", nil)

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, true, body["allow_registration"])
}

func TestSettings_RegistrationDisabled(t *testing.T) {
	srv := setupTestRouterWithConfig(t, false)
	defer srv.Close()

	resp, body := getJSON(srv.URL+"/api/settings", nil)

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, false, body["allow_registration"])
}

func TestChangeEmail_Success(t *testing.T) {
	srv := setupTestRouter(t)
	defer srv.Close()

	regResp, _ := postJSON(srv.URL+"/api/register", map[string]any{
		"username": "alice",
		"email":    "alice@example.com",
		"password": "password123",
	}, nil)
	cookie := findSessionCookie(regResp)
	require.NotNil(t, cookie)

	resp, body := putJSON(srv.URL+"/api/me/email", map[string]any{
		"email": "newalice@example.com",
	}, []*http.Cookie{cookie})

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "newalice@example.com", body["email"])

	// Verify persisted
	_, meBody := getJSON(srv.URL+"/api/me", []*http.Cookie{cookie})
	assert.Equal(t, "newalice@example.com", meBody["email"])
}

func TestChangeEmail_ClearEmail(t *testing.T) {
	srv := setupTestRouter(t)
	defer srv.Close()

	regResp, _ := postJSON(srv.URL+"/api/register", map[string]any{
		"username": "alice",
		"email":    "alice@example.com",
		"password": "password123",
	}, nil)
	cookie := findSessionCookie(regResp)
	require.NotNil(t, cookie)

	resp, body := putJSON(srv.URL+"/api/me/email", map[string]any{
		"email": "",
	}, []*http.Cookie{cookie})

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Nil(t, body["email"])

	// Verify persisted
	_, meBody := getJSON(srv.URL+"/api/me", []*http.Cookie{cookie})
	assert.Nil(t, meBody["email"])
}

func TestChangeEmail_DuplicateEmail(t *testing.T) {
	srv := setupTestRouter(t)
	defer srv.Close()

	postJSON(srv.URL+"/api/register", map[string]any{
		"username": "alice",
		"email":    "alice@example.com",
		"password": "password123",
	}, nil)

	bobResp, _ := postJSON(srv.URL+"/api/register", map[string]any{
		"username": "bob",
		"email":    "bob@example.com",
		"password": "password123",
	}, nil)
	bobCookie := findSessionCookie(bobResp)
	require.NotNil(t, bobCookie)

	resp, body := putJSON(srv.URL+"/api/me/email", map[string]any{
		"email": "alice@example.com",
	}, []*http.Cookie{bobCookie})

	assert.Equal(t, http.StatusConflict, resp.StatusCode)
	assert.Equal(t, "email already in use", body["error"])
}

func TestChangeEmail_Unauthenticated(t *testing.T) {
	srv := setupTestRouter(t)
	defer srv.Close()

	resp, body := putJSON(srv.URL+"/api/me/email", map[string]any{
		"email": "test@example.com",
	}, nil)

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, "authentication required", body["error"])
}

func TestChangePassword_Success(t *testing.T) {
	srv := setupTestRouter(t)
	defer srv.Close()

	regResp, _ := postJSON(srv.URL+"/api/register", map[string]any{
		"username": "alice",
		"password": "password123",
	}, nil)
	cookie := findSessionCookie(regResp)
	require.NotNil(t, cookie)

	resp, body := putJSON(srv.URL+"/api/me/password", map[string]any{
		"current_password": "password123",
		"new_password":     "newpassword456",
	}, []*http.Cookie{cookie})

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "password updated", body["message"])

	// Login with new password succeeds
	loginResp, _ := postJSON(srv.URL+"/api/login", map[string]any{
		"username": "alice",
		"password": "newpassword456",
	}, nil)
	assert.Equal(t, http.StatusOK, loginResp.StatusCode)

	// Login with old password fails
	oldResp, _ := postJSON(srv.URL+"/api/login", map[string]any{
		"username": "alice",
		"password": "password123",
	}, nil)
	assert.Equal(t, http.StatusUnauthorized, oldResp.StatusCode)
}

func TestChangePassword_WrongCurrentPassword(t *testing.T) {
	srv := setupTestRouter(t)
	defer srv.Close()

	regResp, _ := postJSON(srv.URL+"/api/register", map[string]any{
		"username": "alice",
		"password": "password123",
	}, nil)
	cookie := findSessionCookie(regResp)
	require.NotNil(t, cookie)

	resp, body := putJSON(srv.URL+"/api/me/password", map[string]any{
		"current_password": "wrongpassword",
		"new_password":     "newpassword456",
	}, []*http.Cookie{cookie})

	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	assert.Equal(t, "current password is incorrect", body["error"])
}

func TestChangePassword_ShortNewPassword(t *testing.T) {
	srv := setupTestRouter(t)
	defer srv.Close()

	regResp, _ := postJSON(srv.URL+"/api/register", map[string]any{
		"username": "alice",
		"password": "password123",
	}, nil)
	cookie := findSessionCookie(regResp)
	require.NotNil(t, cookie)

	resp, body := putJSON(srv.URL+"/api/me/password", map[string]any{
		"current_password": "password123",
		"new_password":     "short",
	}, []*http.Cookie{cookie})

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Contains(t, body["error"], "password")
}

func TestChangePassword_Unauthenticated(t *testing.T) {
	srv := setupTestRouter(t)
	defer srv.Close()

	resp, body := putJSON(srv.URL+"/api/me/password", map[string]any{
		"current_password": "password123",
		"new_password":     "newpassword456",
	}, nil)

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, "authentication required", body["error"])
}
