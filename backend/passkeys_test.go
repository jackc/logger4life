package backend

import (
	"context"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func insertPasskey(t *testing.T, userID string) {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), "postgres://postgres:postgres@localhost:5432/logger4life_test")
	require.NoError(t, err)
	defer pool.Close()

	_, err = pool.Exec(context.Background(),
		`INSERT INTO passkeys (user_id, credential_id, public_key, aaguid, sign_count, description)
		 VALUES ($1, $2, $3, $4, 0, $5)`,
		userID, []byte("fake-cred-id-1"), []byte("fake-public-key"), []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, "Test Key",
	)
	require.NoError(t, err)
}

func TestListPasskeys_Empty(t *testing.T) {
	srv := setupTestRouter(t)
	defer srv.Close()

	regResp, _ := postJSON(srv.URL+"/api/register", map[string]any{
		"username": "alice",
		"password": "password123",
	}, nil)
	cookie := findSessionCookie(regResp)
	require.NotNil(t, cookie)

	resp, body := getJSONArray(srv.URL+"/api/me/passkeys", []*http.Cookie{cookie})
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, 0, len(body))
}

func TestListPasskeys_WithPasskeys(t *testing.T) {
	srv := setupTestRouter(t)
	defer srv.Close()

	regResp, regBody := postJSON(srv.URL+"/api/register", map[string]any{
		"username": "alice",
		"password": "password123",
	}, nil)
	cookie := findSessionCookie(regResp)
	require.NotNil(t, cookie)

	userID := regBody["id"].(string)
	insertPasskey(t, userID)

	resp, body := getJSONArray(srv.URL+"/api/me/passkeys", []*http.Cookie{cookie})
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, 1, len(body))
	assert.Equal(t, "Test Key", body[0]["description"])
	assert.NotEmpty(t, body[0]["id"])
	assert.NotEmpty(t, body[0]["created_at"])
}

func TestListPasskeys_Unauthenticated(t *testing.T) {
	srv := setupTestRouter(t)
	defer srv.Close()

	resp, _ := getJSONArray(srv.URL+"/api/me/passkeys", nil)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestUpdatePasskeyDescription(t *testing.T) {
	srv := setupTestRouter(t)
	defer srv.Close()

	regResp, regBody := postJSON(srv.URL+"/api/register", map[string]any{
		"username": "alice",
		"password": "password123",
	}, nil)
	cookie := findSessionCookie(regResp)
	require.NotNil(t, cookie)

	userID := regBody["id"].(string)
	insertPasskey(t, userID)

	// Get the passkey ID
	_, listBody := getJSONArray(srv.URL+"/api/me/passkeys", []*http.Cookie{cookie})
	require.Equal(t, 1, len(listBody))
	passkeyID := listBody[0]["id"].(string)

	resp, body := putJSON(srv.URL+"/api/me/passkeys/"+passkeyID, map[string]any{
		"description": "Updated Name",
	}, []*http.Cookie{cookie})
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "Updated Name", body["description"])

	// Verify persisted
	_, listBody2 := getJSONArray(srv.URL+"/api/me/passkeys", []*http.Cookie{cookie})
	assert.Equal(t, "Updated Name", listBody2[0]["description"])
}

func TestUpdatePasskey_NotOwned(t *testing.T) {
	srv := setupTestRouter(t)
	defer srv.Close()

	// Alice registers and adds a passkey
	regResp, regBody := postJSON(srv.URL+"/api/register", map[string]any{
		"username": "alice",
		"password": "password123",
	}, nil)
	aliceCookie := findSessionCookie(regResp)
	require.NotNil(t, aliceCookie)
	insertPasskey(t, regBody["id"].(string))

	// Get Alice's passkey ID
	_, listBody := getJSONArray(srv.URL+"/api/me/passkeys", []*http.Cookie{aliceCookie})
	require.Equal(t, 1, len(listBody))
	passkeyID := listBody[0]["id"].(string)

	// Bob registers
	bobResp, _ := postJSON(srv.URL+"/api/register", map[string]any{
		"username": "bob",
		"password": "password123",
	}, nil)
	bobCookie := findSessionCookie(bobResp)
	require.NotNil(t, bobCookie)

	// Bob tries to update Alice's passkey
	resp, body := putJSON(srv.URL+"/api/me/passkeys/"+passkeyID, map[string]any{
		"description": "Hacked",
	}, []*http.Cookie{bobCookie})
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, "passkey not found", body["error"])
}

func TestDeletePasskey(t *testing.T) {
	srv := setupTestRouter(t)
	defer srv.Close()

	regResp, regBody := postJSON(srv.URL+"/api/register", map[string]any{
		"username": "alice",
		"password": "password123",
	}, nil)
	cookie := findSessionCookie(regResp)
	require.NotNil(t, cookie)
	insertPasskey(t, regBody["id"].(string))

	// Get the passkey ID
	_, listBody := getJSONArray(srv.URL+"/api/me/passkeys", []*http.Cookie{cookie})
	require.Equal(t, 1, len(listBody))
	passkeyID := listBody[0]["id"].(string)

	resp, body := deleteJSON(srv.URL+"/api/me/passkeys/"+passkeyID, []*http.Cookie{cookie})
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "passkey deleted", body["message"])

	// Verify gone
	_, listBody2 := getJSONArray(srv.URL+"/api/me/passkeys", []*http.Cookie{cookie})
	assert.Equal(t, 0, len(listBody2))
}

func TestDeletePasskey_NotOwned(t *testing.T) {
	srv := setupTestRouter(t)
	defer srv.Close()

	regResp, regBody := postJSON(srv.URL+"/api/register", map[string]any{
		"username": "alice",
		"password": "password123",
	}, nil)
	aliceCookie := findSessionCookie(regResp)
	require.NotNil(t, aliceCookie)
	insertPasskey(t, regBody["id"].(string))

	_, listBody := getJSONArray(srv.URL+"/api/me/passkeys", []*http.Cookie{aliceCookie})
	require.Equal(t, 1, len(listBody))
	passkeyID := listBody[0]["id"].(string)

	// Bob registers
	bobResp, _ := postJSON(srv.URL+"/api/register", map[string]any{
		"username": "bob",
		"password": "password123",
	}, nil)
	bobCookie := findSessionCookie(bobResp)
	require.NotNil(t, bobCookie)

	// Bob tries to delete Alice's passkey
	resp, body := deleteJSON(srv.URL+"/api/me/passkeys/"+passkeyID, []*http.Cookie{bobCookie})
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, "passkey not found", body["error"])

	// Verify Alice's passkey still exists
	_, listBody2 := getJSONArray(srv.URL+"/api/me/passkeys", []*http.Cookie{aliceCookie})
	assert.Equal(t, 1, len(listBody2))
}

func TestPasskeyLoginBegin_Success(t *testing.T) {
	srv := setupTestRouter(t)
	defer srv.Close()

	resp, body := postJSON(srv.URL+"/api/passkey-login/begin", map[string]any{}, nil)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.NotEmpty(t, body["challenge_id"])
	assert.NotNil(t, body["options"])
}

func TestPasskeyRegisterBegin_Unauthenticated(t *testing.T) {
	srv := setupTestRouter(t)
	defer srv.Close()

	resp, body := postJSON(srv.URL+"/api/me/passkeys/register/begin", map[string]any{}, nil)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, "authentication required", body["error"])
}

func TestPasskeyRegisterBegin_Success(t *testing.T) {
	srv := setupTestRouter(t)
	defer srv.Close()

	regResp, _ := postJSON(srv.URL+"/api/register", map[string]any{
		"username": "alice",
		"password": "password123",
	}, nil)
	cookie := findSessionCookie(regResp)
	require.NotNil(t, cookie)

	resp, body := postJSON(srv.URL+"/api/me/passkeys/register/begin", map[string]any{}, []*http.Cookie{cookie})
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.NotEmpty(t, body["challenge_id"])
	assert.NotNil(t, body["options"])
}

func TestPasskeyLoginFinish_InvalidChallenge(t *testing.T) {
	srv := setupTestRouter(t)
	defer srv.Close()

	resp, body := postJSON(srv.URL+"/api/passkey-login/finish", map[string]any{
		"challenge_id": "00000000-0000-0000-0000-000000000000",
		"credential":   map[string]any{},
	}, nil)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	assert.Equal(t, "invalid or expired challenge", body["error"])
}

func TestPasskeyRegisterFinish_InvalidChallenge(t *testing.T) {
	srv := setupTestRouter(t)
	defer srv.Close()

	regResp, _ := postJSON(srv.URL+"/api/register", map[string]any{
		"username": "alice",
		"password": "password123",
	}, nil)
	cookie := findSessionCookie(regResp)
	require.NotNil(t, cookie)

	resp, body := postJSON(srv.URL+"/api/me/passkeys/register/finish", map[string]any{
		"challenge_id": "00000000-0000-0000-0000-000000000000",
		"credential":   map[string]any{},
		"description":  "My Key",
	}, []*http.Cookie{cookie})
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, "invalid or expired challenge", body["error"])
}
