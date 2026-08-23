package server

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func getJSONArray(url string, cookies []*http.Cookie) (*http.Response, []map[string]any) {
	req, _ := http.NewRequest("GET", url, nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	client := &http.Client{}
	resp, _ := client.Do(req)
	var result []map[string]any
	json.NewDecoder(resp.Body).Decode(&result)
	resp.Body.Close()
	return resp, result
}

func registerUser(t *testing.T, srvURL, username string) []*http.Cookie {
	t.Helper()
	resp, _ := postJSON(srvURL+"/api/register", map[string]any{
		"username": username,
		"password": "password123",
	}, nil)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	cookie := findSessionCookie(resp)
	require.NotNil(t, cookie)
	return []*http.Cookie{cookie}
}

// --- Create Log ---

func TestCreateLog_Success(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()

	cookies := registerUser(t, srv.URL, "alice")

	resp, body := postJSON(srv.URL+"/api/logs", map[string]any{
		"name": "Vitamins",
	}, cookies)

	assert.Equal(t, http.StatusCreated, resp.StatusCode)
	assert.Equal(t, "Vitamins", body["name"])
	assert.NotEmpty(t, body["id"])
	assert.NotEmpty(t, body["created_at"])
	assert.NotEmpty(t, body["updated_at"])
}

func TestCreateLog_EmptyName(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()

	cookies := registerUser(t, srv.URL, "alice")

	resp, body := postJSON(srv.URL+"/api/logs", map[string]any{
		"name": "",
	}, cookies)

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Contains(t, body["error"], "name")
}

func TestCreateLog_DuplicateName(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()

	cookies := registerUser(t, srv.URL, "alice")

	postJSON(srv.URL+"/api/logs", map[string]any{"name": "Vitamins"}, cookies)

	resp, body := postJSON(srv.URL+"/api/logs", map[string]any{"name": "Vitamins"}, cookies)

	assert.Equal(t, http.StatusConflict, resp.StatusCode)
	assert.Contains(t, body["error"], "already exists")
}

func TestCreateLog_WithFields(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()

	cookies := registerUser(t, srv.URL, "alice")

	resp, body := postJSON(srv.URL+"/api/logs", map[string]any{
		"name": "Pushups",
		"fields": []map[string]any{
			{"name": "count", "type": "number", "required": true},
			{"name": "notes", "type": "text", "required": false},
		},
	}, cookies)

	assert.Equal(t, http.StatusCreated, resp.StatusCode)
	assert.Equal(t, "Pushups", body["name"])

	fields := body["fields"].([]any)
	assert.Len(t, fields, 2)

	f0 := fields[0].(map[string]any)
	assert.Equal(t, "count", f0["name"])
	assert.Equal(t, "number", f0["type"])
	assert.Equal(t, true, f0["required"])

	f1 := fields[1].(map[string]any)
	assert.Equal(t, "notes", f1["name"])
	assert.Equal(t, "text", f1["type"])
	assert.Equal(t, false, f1["required"])
}

func TestCreateLog_NoFieldsReturnsEmptyArray(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()

	cookies := registerUser(t, srv.URL, "alice")

	resp, body := postJSON(srv.URL+"/api/logs", map[string]any{
		"name": "Vitamins",
	}, cookies)

	assert.Equal(t, http.StatusCreated, resp.StatusCode)
	fields := body["fields"].([]any)
	assert.Len(t, fields, 0)
}

func TestCreateLog_InvalidFieldType(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()

	cookies := registerUser(t, srv.URL, "alice")

	resp, body := postJSON(srv.URL+"/api/logs", map[string]any{
		"name": "Test",
		"fields": []map[string]any{
			{"name": "flag", "type": "date"},
		},
	}, cookies)

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Contains(t, body["error"], "type")
}

func TestCreateLog_WithBooleanField(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()

	cookies := registerUser(t, srv.URL, "alice")

	resp, body := postJSON(srv.URL+"/api/logs", map[string]any{
		"name": "Supplements",
		"fields": []map[string]any{
			{"name": "fasted", "type": "boolean", "required": false},
		},
	}, cookies)

	assert.Equal(t, http.StatusCreated, resp.StatusCode)
	fields := body["fields"].([]any)
	assert.Len(t, fields, 1)
	f0 := fields[0].(map[string]any)
	assert.Equal(t, "fasted", f0["name"])
	assert.Equal(t, "boolean", f0["type"])
	assert.Equal(t, false, f0["required"])
}

func TestCreateLog_DuplicateFieldNames(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()

	cookies := registerUser(t, srv.URL, "alice")

	resp, body := postJSON(srv.URL+"/api/logs", map[string]any{
		"name": "Test",
		"fields": []map[string]any{
			{"name": "count", "type": "number"},
			{"name": "count", "type": "text"},
		},
	}, cookies)

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Contains(t, body["error"], "duplicate")
}

func TestCreateLog_EmptyFieldName(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()

	cookies := registerUser(t, srv.URL, "alice")

	resp, body := postJSON(srv.URL+"/api/logs", map[string]any{
		"name": "Test",
		"fields": []map[string]any{
			{"name": "", "type": "number"},
		},
	}, cookies)

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Contains(t, body["error"], "field name")
}

func TestCreateLog_Unauthenticated(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()

	resp, _ := postJSON(srv.URL+"/api/logs", map[string]any{"name": "Vitamins"}, nil)

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

// --- List Logs ---

func TestListLogs_Empty(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()

	cookies := registerUser(t, srv.URL, "alice")

	resp, body := getJSONArray(srv.URL+"/api/logs", cookies)

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Len(t, body, 0)
}

func TestListLogs_ReturnUserLogs(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()

	cookies := registerUser(t, srv.URL, "alice")

	postJSON(srv.URL+"/api/logs", map[string]any{"name": "Pushups"}, cookies)
	postJSON(srv.URL+"/api/logs", map[string]any{"name": "Vitamins"}, cookies)

	resp, body := getJSONArray(srv.URL+"/api/logs", cookies)

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Len(t, body, 2)
	assert.Equal(t, "Pushups", body[0]["name"])
	assert.Equal(t, "Vitamins", body[1]["name"])
}

func TestListLogs_DoesNotReturnOtherUsersLogs(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()

	aliceCookies := registerUser(t, srv.URL, "alice")
	bobCookies := registerUser(t, srv.URL, "bob")

	postJSON(srv.URL+"/api/logs", map[string]any{"name": "Alice Log"}, aliceCookies)

	resp, body := getJSONArray(srv.URL+"/api/logs", bobCookies)

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Len(t, body, 0)
}

// --- Get Log ---

func TestGetLog_Success(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()

	cookies := registerUser(t, srv.URL, "alice")

	_, created := postJSON(srv.URL+"/api/logs", map[string]any{"name": "Vitamins"}, cookies)
	logID := created["id"].(string)

	resp, body := getJSON(srv.URL+"/api/logs/"+logID, cookies)

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "Vitamins", body["name"])
	assert.Equal(t, logID, body["id"])
}

func TestGetLog_NotFound(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()

	cookies := registerUser(t, srv.URL, "alice")

	resp, body := getJSON(srv.URL+"/api/logs/00000000-0000-0000-0000-000000000000", cookies)

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, "log not found", body["error"])
}

func TestGetLog_OtherUsersLog(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()

	aliceCookies := registerUser(t, srv.URL, "alice")
	bobCookies := registerUser(t, srv.URL, "bob")

	_, created := postJSON(srv.URL+"/api/logs", map[string]any{"name": "Alice Log"}, aliceCookies)
	logID := created["id"].(string)

	resp, body := getJSON(srv.URL+"/api/logs/"+logID, bobCookies)

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, "log not found", body["error"])
}

func TestGetLog_ReturnsFields(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()

	cookies := registerUser(t, srv.URL, "alice")

	_, created := postJSON(srv.URL+"/api/logs", map[string]any{
		"name": "Pushups",
		"fields": []map[string]any{
			{"name": "count", "type": "number", "required": true},
		},
	}, cookies)
	logID := created["id"].(string)

	resp, body := getJSON(srv.URL+"/api/logs/"+logID, cookies)

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	fields := body["fields"].([]any)
	assert.Len(t, fields, 1)
	f0 := fields[0].(map[string]any)
	assert.Equal(t, "count", f0["name"])
	assert.Equal(t, "number", f0["type"])
	assert.Equal(t, true, f0["required"])
}

// --- Update Log ---

func TestUpdateLog_RenameSucess(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()

	cookies := registerUser(t, srv.URL, "alice")

	_, created := postJSON(srv.URL+"/api/logs", map[string]any{"name": "Vitamins"}, cookies)
	logID := created["id"].(string)

	resp, body := putJSON(srv.URL+"/api/logs/"+logID, map[string]any{
		"name": "Supplements",
	}, cookies)

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "Supplements", body["name"])
	assert.Equal(t, logID, body["id"])
	assert.Equal(t, true, body["is_owner"])
	assert.NotEmpty(t, body["updated_at"])
}

func TestUpdateLog_UpdateFields(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()

	cookies := registerUser(t, srv.URL, "alice")

	_, created := postJSON(srv.URL+"/api/logs", map[string]any{
		"name": "Pushups",
		"fields": []map[string]any{
			{"name": "count", "type": "number", "required": true},
		},
	}, cookies)
	logID := created["id"].(string)

	resp, body := putJSON(srv.URL+"/api/logs/"+logID, map[string]any{
		"name": "Pushups",
		"fields": []map[string]any{
			{"name": "reps", "type": "number", "required": true},
			{"name": "notes", "type": "text", "required": false},
		},
	}, cookies)

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	fields := body["fields"].([]any)
	assert.Len(t, fields, 2)
	f0 := fields[0].(map[string]any)
	assert.Equal(t, "reps", f0["name"])
	assert.Equal(t, "number", f0["type"])
	assert.Equal(t, true, f0["required"])
	f1 := fields[1].(map[string]any)
	assert.Equal(t, "notes", f1["name"])
	assert.Equal(t, "text", f1["type"])
	assert.Equal(t, false, f1["required"])
}

func TestUpdateLog_SameNameAllowed(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()

	cookies := registerUser(t, srv.URL, "alice")

	_, created := postJSON(srv.URL+"/api/logs", map[string]any{"name": "Vitamins"}, cookies)
	logID := created["id"].(string)

	resp, body := putJSON(srv.URL+"/api/logs/"+logID, map[string]any{
		"name": "Vitamins",
	}, cookies)

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "Vitamins", body["name"])
}

func TestUpdateLog_EmptyName(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()

	cookies := registerUser(t, srv.URL, "alice")

	_, created := postJSON(srv.URL+"/api/logs", map[string]any{"name": "Vitamins"}, cookies)
	logID := created["id"].(string)

	resp, body := putJSON(srv.URL+"/api/logs/"+logID, map[string]any{
		"name": "",
	}, cookies)

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Contains(t, body["error"], "name")
}

func TestUpdateLog_DuplicateName(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()

	cookies := registerUser(t, srv.URL, "alice")

	postJSON(srv.URL+"/api/logs", map[string]any{"name": "Vitamins"}, cookies)
	_, created := postJSON(srv.URL+"/api/logs", map[string]any{"name": "Pushups"}, cookies)
	logID := created["id"].(string)

	resp, body := putJSON(srv.URL+"/api/logs/"+logID, map[string]any{
		"name": "Vitamins",
	}, cookies)

	assert.Equal(t, http.StatusConflict, resp.StatusCode)
	assert.Contains(t, body["error"], "already exists")
}

func TestUpdateLog_InvalidFieldType(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()

	cookies := registerUser(t, srv.URL, "alice")

	_, created := postJSON(srv.URL+"/api/logs", map[string]any{"name": "Test"}, cookies)
	logID := created["id"].(string)

	resp, body := putJSON(srv.URL+"/api/logs/"+logID, map[string]any{
		"name": "Test",
		"fields": []map[string]any{
			{"name": "flag", "type": "date"},
		},
	}, cookies)

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Contains(t, body["error"], "type")
}

func TestUpdateLog_DuplicateFieldNames(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()

	cookies := registerUser(t, srv.URL, "alice")

	_, created := postJSON(srv.URL+"/api/logs", map[string]any{"name": "Test"}, cookies)
	logID := created["id"].(string)

	resp, body := putJSON(srv.URL+"/api/logs/"+logID, map[string]any{
		"name": "Test",
		"fields": []map[string]any{
			{"name": "count", "type": "number"},
			{"name": "count", "type": "text"},
		},
	}, cookies)

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Contains(t, body["error"], "duplicate")
}

func TestUpdateLog_NotFound(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()

	cookies := registerUser(t, srv.URL, "alice")

	resp, body := putJSON(srv.URL+"/api/logs/00000000-0000-0000-0000-000000000000", map[string]any{
		"name": "Test",
	}, cookies)

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, "log not found", body["error"])
}

func TestUpdateLog_OtherUsersLog(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()

	aliceCookies := registerUser(t, srv.URL, "alice")
	bobCookies := registerUser(t, srv.URL, "bob")

	_, created := postJSON(srv.URL+"/api/logs", map[string]any{"name": "Alice Log"}, aliceCookies)
	logID := created["id"].(string)

	resp, body := putJSON(srv.URL+"/api/logs/"+logID, map[string]any{
		"name": "Stolen Log",
	}, bobCookies)

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, "log not found", body["error"])
}

func TestUpdateLog_SharedUserCannotEdit(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()

	aliceCookies := registerUser(t, srv.URL, "alice")
	bobCookies := registerUser(t, srv.URL, "bob")

	_, created := postJSON(srv.URL+"/api/logs", map[string]any{"name": "Alice Log"}, aliceCookies)
	logID := created["id"].(string)

	// Generate share token and have Bob join
	_, tokenBody := postJSON(srv.URL+"/api/logs/"+logID+"/share-token", map[string]any{}, aliceCookies)
	token := tokenBody["share_token"].(string)
	postJSON(srv.URL+"/api/join/"+token, map[string]any{}, bobCookies)

	// Bob tries to update the log
	resp, body := putJSON(srv.URL+"/api/logs/"+logID, map[string]any{
		"name": "Bobs Log Now",
	}, bobCookies)

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, "log not found", body["error"])
}

func TestUpdateLog_Unauthenticated(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()

	resp, _ := putJSON(srv.URL+"/api/logs/00000000-0000-0000-0000-000000000000", map[string]any{
		"name": "Test",
	}, nil)

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestUpdateLog_ExistingEntriesUntouched(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()

	cookies := registerUser(t, srv.URL, "alice")

	// Create log with "count" field
	_, created := postJSON(srv.URL+"/api/logs", map[string]any{
		"name": "Exercise",
		"fields": []map[string]any{
			{"name": "count", "type": "number", "required": true},
		},
	}, cookies)
	logID := created["id"].(string)

	// Create an entry with the old field
	postJSON(srv.URL+"/api/logs/"+logID+"/entries", map[string]any{
		"fields": map[string]any{"count": "25"},
	}, cookies)

	// Update log to use different fields
	putJSON(srv.URL+"/api/logs/"+logID, map[string]any{
		"name": "Exercise",
		"fields": []map[string]any{
			{"name": "reps", "type": "number", "required": true},
		},
	}, cookies)

	// Old entry still has its original data
	_, entries := getJSONArray(srv.URL+"/api/logs/"+logID+"/entries", cookies)
	assert.Len(t, entries, 1)
	fields := entries[0]["fields"].(map[string]any)
	assert.Equal(t, "25", fields["count"])

	// New entries must use the new field definitions
	resp, body := postJSON(srv.URL+"/api/logs/"+logID+"/entries", map[string]any{
		"fields": map[string]any{"count": "30"},
	}, cookies)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Contains(t, body["error"], "unknown")

	// New entry with correct field works
	resp2, _ := postJSON(srv.URL+"/api/logs/"+logID+"/entries", map[string]any{
		"fields": map[string]any{"reps": "30"},
	}, cookies)
	assert.Equal(t, http.StatusCreated, resp2.StatusCode)
}

// --- Create Log Entry ---

func TestCreateLogEntry_Success(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()

	cookies := registerUser(t, srv.URL, "alice")

	_, created := postJSON(srv.URL+"/api/logs", map[string]any{"name": "Vitamins"}, cookies)
	logID := created["id"].(string)

	resp, body := postJSON(srv.URL+"/api/logs/"+logID+"/entries", map[string]any{}, cookies)

	assert.Equal(t, http.StatusCreated, resp.StatusCode)
	assert.NotEmpty(t, body["id"])
	assert.Equal(t, logID, body["log_id"])
	assert.NotEmpty(t, body["user_id"])
	assert.Equal(t, "alice", body["username"])
	assert.NotEmpty(t, body["occurred_at"])
	assert.NotEmpty(t, body["created_at"])
	assert.NotEmpty(t, body["updated_at"])
}

func TestCreateLogEntry_NonexistentLog(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()

	cookies := registerUser(t, srv.URL, "alice")

	resp, body := postJSON(srv.URL+"/api/logs/00000000-0000-0000-0000-000000000000/entries", map[string]any{}, cookies)

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, "log not found", body["error"])
}

func TestCreateLogEntry_OtherUsersLog(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()

	aliceCookies := registerUser(t, srv.URL, "alice")
	bobCookies := registerUser(t, srv.URL, "bob")

	_, created := postJSON(srv.URL+"/api/logs", map[string]any{"name": "Alice Log"}, aliceCookies)
	logID := created["id"].(string)

	resp, body := postJSON(srv.URL+"/api/logs/"+logID+"/entries", map[string]any{}, bobCookies)

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, "log not found", body["error"])
}

func TestCreateLogEntry_WithFields(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()

	cookies := registerUser(t, srv.URL, "alice")

	_, created := postJSON(srv.URL+"/api/logs", map[string]any{
		"name": "Pushups",
		"fields": []map[string]any{
			{"name": "count", "type": "number", "required": true},
			{"name": "notes", "type": "text", "required": false},
		},
	}, cookies)
	logID := created["id"].(string)

	resp, body := postJSON(srv.URL+"/api/logs/"+logID+"/entries", map[string]any{
		"fields": map[string]any{
			"count": "25",
			"notes": "morning set",
		},
	}, cookies)

	assert.Equal(t, http.StatusCreated, resp.StatusCode)
	assert.NotEmpty(t, body["id"])
	assert.Equal(t, logID, body["log_id"])
	assert.NotEmpty(t, body["user_id"])
	assert.Equal(t, "alice", body["username"])

	fields := body["fields"].(map[string]any)
	assert.Equal(t, "25", fields["count"])
	assert.Equal(t, "morning set", fields["notes"])
}

func TestCreateLogEntry_WrongFieldType(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()

	cookies := registerUser(t, srv.URL, "alice")

	_, created := postJSON(srv.URL+"/api/logs", map[string]any{
		"name": "Pushups",
		"fields": []map[string]any{
			{"name": "count", "type": "number", "required": true},
		},
	}, cookies)
	logID := created["id"].(string)

	resp, body := postJSON(srv.URL+"/api/logs/"+logID+"/entries", map[string]any{
		"fields": map[string]any{
			"count": "not a number",
		},
	}, cookies)

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Contains(t, body["error"], "number")
}

func TestCreateLogEntry_UnknownField(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()

	cookies := registerUser(t, srv.URL, "alice")

	_, created := postJSON(srv.URL+"/api/logs", map[string]any{
		"name": "Pushups",
		"fields": []map[string]any{
			{"name": "count", "type": "number"},
		},
	}, cookies)
	logID := created["id"].(string)

	resp, body := postJSON(srv.URL+"/api/logs/"+logID+"/entries", map[string]any{
		"fields": map[string]any{
			"unknown": "5",
		},
	}, cookies)

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Contains(t, body["error"], "unknown")
}

func TestCreateLogEntry_MissingRequiredField(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()

	cookies := registerUser(t, srv.URL, "alice")

	_, created := postJSON(srv.URL+"/api/logs", map[string]any{
		"name": "Pushups",
		"fields": []map[string]any{
			{"name": "count", "type": "number", "required": true},
		},
	}, cookies)
	logID := created["id"].(string)

	resp, body := postJSON(srv.URL+"/api/logs/"+logID+"/entries", map[string]any{
		"fields": map[string]any{},
	}, cookies)

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Contains(t, body["error"], "required")
}

func TestCreateLogEntry_WithBooleanField(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()

	cookies := registerUser(t, srv.URL, "alice")

	_, created := postJSON(srv.URL+"/api/logs", map[string]any{
		"name": "Supplements",
		"fields": []map[string]any{
			{"name": "fasted", "type": "boolean", "required": false},
		},
	}, cookies)
	logID := created["id"].(string)

	resp, body := postJSON(srv.URL+"/api/logs/"+logID+"/entries", map[string]any{
		"fields": map[string]any{
			"fasted": true,
		},
	}, cookies)

	assert.Equal(t, http.StatusCreated, resp.StatusCode)
	fields := body["fields"].(map[string]any)
	assert.Equal(t, true, fields["fasted"])

	// Also test with false
	resp2, body2 := postJSON(srv.URL+"/api/logs/"+logID+"/entries", map[string]any{
		"fields": map[string]any{
			"fasted": false,
		},
	}, cookies)

	assert.Equal(t, http.StatusCreated, resp2.StatusCode)
	fields2 := body2["fields"].(map[string]any)
	assert.Equal(t, false, fields2["fasted"])
}

func TestCreateLogEntry_InvalidBooleanValue(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()

	cookies := registerUser(t, srv.URL, "alice")

	_, created := postJSON(srv.URL+"/api/logs", map[string]any{
		"name": "Supplements",
		"fields": []map[string]any{
			{"name": "fasted", "type": "boolean", "required": false},
		},
	}, cookies)
	logID := created["id"].(string)

	resp, body := postJSON(srv.URL+"/api/logs/"+logID+"/entries", map[string]any{
		"fields": map[string]any{
			"fasted": "true",
		},
	}, cookies)

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Contains(t, body["error"], "true or false")
}

func TestCreateLogEntry_OptionalFieldOmitted(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()

	cookies := registerUser(t, srv.URL, "alice")

	_, created := postJSON(srv.URL+"/api/logs", map[string]any{
		"name": "Pushups",
		"fields": []map[string]any{
			{"name": "count", "type": "number", "required": true},
			{"name": "notes", "type": "text", "required": false},
		},
	}, cookies)
	logID := created["id"].(string)

	resp, body := postJSON(srv.URL+"/api/logs/"+logID+"/entries", map[string]any{
		"fields": map[string]any{
			"count": "10",
		},
	}, cookies)

	assert.Equal(t, http.StatusCreated, resp.StatusCode)
	assert.NotEmpty(t, body["id"])
}

// --- List Log Entries ---

func TestListLogEntries_Empty(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()

	cookies := registerUser(t, srv.URL, "alice")

	_, created := postJSON(srv.URL+"/api/logs", map[string]any{"name": "Vitamins"}, cookies)
	logID := created["id"].(string)

	resp, body := getJSONArray(srv.URL+"/api/logs/"+logID+"/entries", cookies)

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Len(t, body, 0)
}

func TestListLogEntries_ReturnsEntries(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()

	cookies := registerUser(t, srv.URL, "alice")

	_, created := postJSON(srv.URL+"/api/logs", map[string]any{"name": "Vitamins"}, cookies)
	logID := created["id"].(string)

	postJSON(srv.URL+"/api/logs/"+logID+"/entries", map[string]any{}, cookies)
	postJSON(srv.URL+"/api/logs/"+logID+"/entries", map[string]any{}, cookies)

	resp, body := getJSONArray(srv.URL+"/api/logs/"+logID+"/entries", cookies)

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Len(t, body, 2)
	assert.Equal(t, "alice", body[0]["username"])
	assert.Equal(t, "alice", body[1]["username"])
}

func TestListLogEntries_OtherUsersLog(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()

	aliceCookies := registerUser(t, srv.URL, "alice")
	bobCookies := registerUser(t, srv.URL, "bob")

	_, created := postJSON(srv.URL+"/api/logs", map[string]any{"name": "Alice Log"}, aliceCookies)
	logID := created["id"].(string)

	resp, _ := getJSONArray(srv.URL+"/api/logs/"+logID+"/entries", bobCookies)

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestListLogEntries_ReturnsFields(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()

	cookies := registerUser(t, srv.URL, "alice")

	_, created := postJSON(srv.URL+"/api/logs", map[string]any{
		"name": "Pushups",
		"fields": []map[string]any{
			{"name": "count", "type": "number", "required": true},
		},
	}, cookies)
	logID := created["id"].(string)

	postJSON(srv.URL+"/api/logs/"+logID+"/entries", map[string]any{
		"fields": map[string]any{"count": "25"},
	}, cookies)
	postJSON(srv.URL+"/api/logs/"+logID+"/entries", map[string]any{
		"fields": map[string]any{"count": "30"},
	}, cookies)

	resp, body := getJSONArray(srv.URL+"/api/logs/"+logID+"/entries", cookies)

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Len(t, body, 2)

	// Entries are ordered newest first
	fields0 := body[0]["fields"].(map[string]any)
	assert.Equal(t, "30", fields0["count"])

	fields1 := body[1]["fields"].(map[string]any)
	assert.Equal(t, "25", fields1["count"])
}

// --- Update Log Entry ---

func TestUpdateLogEntry_Success(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()

	cookies := registerUser(t, srv.URL, "alice")

	_, created := postJSON(srv.URL+"/api/logs", map[string]any{"name": "Vitamins"}, cookies)
	logID := created["id"].(string)

	_, entry := postJSON(srv.URL+"/api/logs/"+logID+"/entries", map[string]any{}, cookies)
	entryID := entry["id"].(string)

	newTime := "2025-06-15T10:30:00Z"
	resp, body := putJSON(srv.URL+"/api/logs/"+logID+"/entries/"+entryID, map[string]any{
		"fields":      map[string]any{},
		"occurred_at": newTime,
	}, cookies)

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, entryID, body["id"])
	assert.Equal(t, logID, body["log_id"])
	assert.NotEmpty(t, body["user_id"])
	assert.Equal(t, "alice", body["username"])
	occurredAt, err := time.Parse(time.RFC3339, body["occurred_at"].(string))
	require.NoError(t, err)
	expectedOccurredAt, err := time.Parse(time.RFC3339, newTime)
	require.NoError(t, err)
	assert.WithinDuration(t, expectedOccurredAt, occurredAt, 0)
	assert.NotEmpty(t, body["created_at"])
	assert.NotEmpty(t, body["updated_at"])
}

func TestUpdateLogEntry_WithFields(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()

	cookies := registerUser(t, srv.URL, "alice")

	_, created := postJSON(srv.URL+"/api/logs", map[string]any{
		"name": "Pushups",
		"fields": []map[string]any{
			{"name": "count", "type": "number", "required": true},
			{"name": "notes", "type": "text", "required": false},
		},
	}, cookies)
	logID := created["id"].(string)

	_, entry := postJSON(srv.URL+"/api/logs/"+logID+"/entries", map[string]any{
		"fields": map[string]any{
			"count": "25",
			"notes": "morning set",
		},
	}, cookies)
	entryID := entry["id"].(string)

	newTime := "2025-06-15T10:30:00Z"
	resp, body := putJSON(srv.URL+"/api/logs/"+logID+"/entries/"+entryID, map[string]any{
		"fields": map[string]any{
			"count": "30",
			"notes": "evening set",
		},
		"occurred_at": newTime,
	}, cookies)

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.NotEmpty(t, body["user_id"])
	assert.Equal(t, "alice", body["username"])
	fields := body["fields"].(map[string]any)
	assert.Equal(t, "30", fields["count"])
	assert.Equal(t, "evening set", fields["notes"])
}

func TestUpdateLogEntry_NonexistentEntry(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()

	cookies := registerUser(t, srv.URL, "alice")

	_, created := postJSON(srv.URL+"/api/logs", map[string]any{"name": "Vitamins"}, cookies)
	logID := created["id"].(string)

	resp, body := putJSON(srv.URL+"/api/logs/"+logID+"/entries/00000000-0000-0000-0000-000000000000", map[string]any{
		"fields":      map[string]any{},
		"occurred_at": "2025-06-15T10:30:00Z",
	}, cookies)

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, "entry not found", body["error"])
}

func TestUpdateLogEntry_NonexistentLog(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()

	cookies := registerUser(t, srv.URL, "alice")

	resp, body := putJSON(srv.URL+"/api/logs/00000000-0000-0000-0000-000000000000/entries/00000000-0000-0000-0000-000000000000", map[string]any{
		"fields":      map[string]any{},
		"occurred_at": "2025-06-15T10:30:00Z",
	}, cookies)

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, "log not found", body["error"])
}

func TestUpdateLogEntry_OtherUsersLog(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()

	aliceCookies := registerUser(t, srv.URL, "alice")
	bobCookies := registerUser(t, srv.URL, "bob")

	_, created := postJSON(srv.URL+"/api/logs", map[string]any{"name": "Alice Log"}, aliceCookies)
	logID := created["id"].(string)

	_, entry := postJSON(srv.URL+"/api/logs/"+logID+"/entries", map[string]any{}, aliceCookies)
	entryID := entry["id"].(string)

	resp, body := putJSON(srv.URL+"/api/logs/"+logID+"/entries/"+entryID, map[string]any{
		"fields":      map[string]any{},
		"occurred_at": "2025-06-15T10:30:00Z",
	}, bobCookies)

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, "log not found", body["error"])
}

func TestUpdateLogEntry_InvalidFieldValues(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()

	cookies := registerUser(t, srv.URL, "alice")

	_, created := postJSON(srv.URL+"/api/logs", map[string]any{
		"name": "Pushups",
		"fields": []map[string]any{
			{"name": "count", "type": "number", "required": true},
		},
	}, cookies)
	logID := created["id"].(string)

	_, entry := postJSON(srv.URL+"/api/logs/"+logID+"/entries", map[string]any{
		"fields": map[string]any{"count": "25"},
	}, cookies)
	entryID := entry["id"].(string)

	resp, body := putJSON(srv.URL+"/api/logs/"+logID+"/entries/"+entryID, map[string]any{
		"fields": map[string]any{
			"count": "not a number",
		},
		"occurred_at": "2025-06-15T10:30:00Z",
	}, cookies)

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Contains(t, body["error"], "number")
}

func TestUpdateLogEntry_MissingRequiredField(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()

	cookies := registerUser(t, srv.URL, "alice")

	_, created := postJSON(srv.URL+"/api/logs", map[string]any{
		"name": "Pushups",
		"fields": []map[string]any{
			{"name": "count", "type": "number", "required": true},
		},
	}, cookies)
	logID := created["id"].(string)

	_, entry := postJSON(srv.URL+"/api/logs/"+logID+"/entries", map[string]any{
		"fields": map[string]any{"count": "25"},
	}, cookies)
	entryID := entry["id"].(string)

	resp, body := putJSON(srv.URL+"/api/logs/"+logID+"/entries/"+entryID, map[string]any{
		"fields":      map[string]any{},
		"occurred_at": "2025-06-15T10:30:00Z",
	}, cookies)

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Contains(t, body["error"], "required")
}

func TestUpdateLogEntry_MissingOccurredAt(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()

	cookies := registerUser(t, srv.URL, "alice")

	_, created := postJSON(srv.URL+"/api/logs", map[string]any{"name": "Vitamins"}, cookies)
	logID := created["id"].(string)

	_, entry := postJSON(srv.URL+"/api/logs/"+logID+"/entries", map[string]any{}, cookies)
	entryID := entry["id"].(string)

	resp, body := putJSON(srv.URL+"/api/logs/"+logID+"/entries/"+entryID, map[string]any{
		"fields": map[string]any{},
	}, cookies)

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, "occurred_at is required", body["error"])
}

func TestUpdateLogEntry_Unauthenticated(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()

	resp, _ := putJSON(srv.URL+"/api/logs/00000000-0000-0000-0000-000000000000/entries/00000000-0000-0000-0000-000000000000", map[string]any{
		"fields":      map[string]any{},
		"occurred_at": "2025-06-15T10:30:00Z",
	}, nil)

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

// --- Delete Log Entry ---

func TestDeleteLogEntry_Success(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()

	cookies := registerUser(t, srv.URL, "alice")

	_, created := postJSON(srv.URL+"/api/logs", map[string]any{"name": "Vitamins"}, cookies)
	logID := created["id"].(string)

	_, entry := postJSON(srv.URL+"/api/logs/"+logID+"/entries", map[string]any{}, cookies)
	entryID := entry["id"].(string)

	resp, _ := deleteJSON(srv.URL+"/api/logs/"+logID+"/entries/"+entryID, cookies)

	assert.Equal(t, http.StatusNoContent, resp.StatusCode)

	// Verify entry is gone
	listResp, entries := getJSONArray(srv.URL+"/api/logs/"+logID+"/entries", cookies)
	assert.Equal(t, http.StatusOK, listResp.StatusCode)
	assert.Len(t, entries, 0)
}

func TestDeleteLogEntry_NonexistentEntry(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()

	cookies := registerUser(t, srv.URL, "alice")

	_, created := postJSON(srv.URL+"/api/logs", map[string]any{"name": "Vitamins"}, cookies)
	logID := created["id"].(string)

	resp, body := deleteJSON(srv.URL+"/api/logs/"+logID+"/entries/00000000-0000-0000-0000-000000000000", cookies)

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, "entry not found", body["error"])
}

func TestDeleteLogEntry_NonexistentLog(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()

	cookies := registerUser(t, srv.URL, "alice")

	resp, body := deleteJSON(srv.URL+"/api/logs/00000000-0000-0000-0000-000000000000/entries/00000000-0000-0000-0000-000000000000", cookies)

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, "log not found", body["error"])
}

func TestDeleteLogEntry_OtherUsersLog(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()

	aliceCookies := registerUser(t, srv.URL, "alice")
	bobCookies := registerUser(t, srv.URL, "bob")

	_, created := postJSON(srv.URL+"/api/logs", map[string]any{"name": "Alice Log"}, aliceCookies)
	logID := created["id"].(string)

	_, entry := postJSON(srv.URL+"/api/logs/"+logID+"/entries", map[string]any{}, aliceCookies)
	entryID := entry["id"].(string)

	resp, body := deleteJSON(srv.URL+"/api/logs/"+logID+"/entries/"+entryID, bobCookies)

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, "log not found", body["error"])
}

func TestDeleteLogEntry_Unauthenticated(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()

	resp, _ := deleteJSON(srv.URL+"/api/logs/00000000-0000-0000-0000-000000000000/entries/00000000-0000-0000-0000-000000000000", nil)

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

// --- Delete Log ---

func TestDeleteLog_Success(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()

	cookies := registerUser(t, srv.URL, "alice")

	_, created := postJSON(srv.URL+"/api/logs", map[string]any{"name": "Vitamins"}, cookies)
	logID := created["id"].(string)

	resp, _ := deleteJSON(srv.URL+"/api/logs/"+logID, cookies)

	assert.Equal(t, http.StatusNoContent, resp.StatusCode)

	// Verify log is gone
	listResp, logs := getJSONArray(srv.URL+"/api/logs", cookies)
	assert.Equal(t, http.StatusOK, listResp.StatusCode)
	assert.Len(t, logs, 0)
}

func TestDeleteLog_AlsoDeletesEntries(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()

	cookies := registerUser(t, srv.URL, "alice")

	_, created := postJSON(srv.URL+"/api/logs", map[string]any{"name": "Vitamins"}, cookies)
	logID := created["id"].(string)

	// Create some entries
	postJSON(srv.URL+"/api/logs/"+logID+"/entries", map[string]any{}, cookies)
	postJSON(srv.URL+"/api/logs/"+logID+"/entries", map[string]any{}, cookies)

	// Delete the log
	resp, _ := deleteJSON(srv.URL+"/api/logs/"+logID, cookies)
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)

	// Verify log is gone from list
	listResp, logs := getJSONArray(srv.URL+"/api/logs", cookies)
	assert.Equal(t, http.StatusOK, listResp.StatusCode)
	assert.Len(t, logs, 0)
}

func TestDeleteLog_NotFound(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()

	cookies := registerUser(t, srv.URL, "alice")

	resp, body := deleteJSON(srv.URL+"/api/logs/00000000-0000-0000-0000-000000000000", cookies)

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, "log not found", body["error"])
}

func TestDeleteLog_OtherUsersLog(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()

	aliceCookies := registerUser(t, srv.URL, "alice")
	bobCookies := registerUser(t, srv.URL, "bob")

	_, created := postJSON(srv.URL+"/api/logs", map[string]any{"name": "Alice Log"}, aliceCookies)
	logID := created["id"].(string)

	resp, body := deleteJSON(srv.URL+"/api/logs/"+logID, bobCookies)

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, "log not found", body["error"])

	// Verify Alice's log still exists
	listResp, logs := getJSONArray(srv.URL+"/api/logs", aliceCookies)
	assert.Equal(t, http.StatusOK, listResp.StatusCode)
	assert.Len(t, logs, 1)
}

func TestDeleteLog_Unauthenticated(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()

	resp, _ := deleteJSON(srv.URL+"/api/logs/00000000-0000-0000-0000-000000000000", nil)

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

// --- Entry Creator Tracking ---

func TestCreateLogEntry_SharedUser_TracksCorrectUser(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()

	aliceCookies := registerUser(t, srv.URL, "alice")
	bobCookies := registerUser(t, srv.URL, "bob")

	_, created := postJSON(srv.URL+"/api/logs", map[string]any{"name": "Shared Log"}, aliceCookies)
	logID := created["id"].(string)

	_, tokenBody := postJSON(srv.URL+"/api/logs/"+logID+"/share-token", map[string]any{}, aliceCookies)
	token := tokenBody["share_token"].(string)
	postJSON(srv.URL+"/api/join/"+token, map[string]any{}, bobCookies)

	resp, body := postJSON(srv.URL+"/api/logs/"+logID+"/entries", map[string]any{}, bobCookies)

	assert.Equal(t, http.StatusCreated, resp.StatusCode)
	assert.Equal(t, "bob", body["username"])
}

func TestListLogEntries_SharedLog_ShowsCorrectUsers(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()

	aliceCookies := registerUser(t, srv.URL, "alice")
	bobCookies := registerUser(t, srv.URL, "bob")

	_, created := postJSON(srv.URL+"/api/logs", map[string]any{"name": "Shared Log"}, aliceCookies)
	logID := created["id"].(string)

	_, tokenBody := postJSON(srv.URL+"/api/logs/"+logID+"/share-token", map[string]any{}, aliceCookies)
	token := tokenBody["share_token"].(string)
	postJSON(srv.URL+"/api/join/"+token, map[string]any{}, bobCookies)

	postJSON(srv.URL+"/api/logs/"+logID+"/entries", map[string]any{}, aliceCookies)
	postJSON(srv.URL+"/api/logs/"+logID+"/entries", map[string]any{}, bobCookies)

	resp, entries := getJSONArray(srv.URL+"/api/logs/"+logID+"/entries", aliceCookies)

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Len(t, entries, 2)
	// Entries ordered by occurred_at DESC, bob's entry (created second) is first
	assert.Equal(t, "bob", entries[0]["username"])
	assert.Equal(t, "alice", entries[1]["username"])
}

func TestUpdateLogEntry_PreservesOriginalCreator(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()

	aliceCookies := registerUser(t, srv.URL, "alice")
	bobCookies := registerUser(t, srv.URL, "bob")

	_, created := postJSON(srv.URL+"/api/logs", map[string]any{"name": "Shared Log"}, aliceCookies)
	logID := created["id"].(string)

	_, tokenBody := postJSON(srv.URL+"/api/logs/"+logID+"/share-token", map[string]any{}, aliceCookies)
	token := tokenBody["share_token"].(string)
	postJSON(srv.URL+"/api/join/"+token, map[string]any{}, bobCookies)

	// Alice creates an entry
	_, entry := postJSON(srv.URL+"/api/logs/"+logID+"/entries", map[string]any{}, aliceCookies)
	entryID := entry["id"].(string)

	// Bob updates it
	resp, body := putJSON(srv.URL+"/api/logs/"+logID+"/entries/"+entryID, map[string]any{
		"fields":      map[string]any{},
		"occurred_at": "2025-06-15T10:30:00Z",
	}, bobCookies)

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "alice", body["username"])
}

// --- Placement ---

func TestCreateLog_AssignsPlacement(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()

	cookies := registerUser(t, srv.URL, "alice")

	_, a := postJSON(srv.URL+"/api/logs", map[string]any{"name": "A"}, cookies)
	_, b := postJSON(srv.URL+"/api/logs", map[string]any{"name": "B"}, cookies)

	assert.Nil(t, a["folder_id"])
	assert.EqualValues(t, 0, a["position"])
	assert.Nil(t, b["folder_id"])
	assert.EqualValues(t, 1, b["position"])
}

func TestListLogs_OrderedByPlacement(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()

	cookies := registerUser(t, srv.URL, "alice")

	// Create alphabetically out of order.
	_, c := postJSON(srv.URL+"/api/logs", map[string]any{"name": "C"}, cookies)
	postJSON(srv.URL+"/api/logs", map[string]any{"name": "A"}, cookies)
	postJSON(srv.URL+"/api/logs", map[string]any{"name": "B"}, cookies)

	_, body := getJSONArray(srv.URL+"/api/logs", cookies)
	require.Len(t, body, 3)
	// Order follows insertion (positions 0,1,2), not alphabetical.
	assert.Equal(t, c["id"], body[0]["id"])
	assert.Equal(t, "A", body[1]["name"])
	assert.Equal(t, "B", body[2]["name"])
}

func TestUpdateLogPlacement_MoveIntoFolder(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()

	cookies := registerUser(t, srv.URL, "alice")

	_, folder := postJSON(srv.URL+"/api/folders", map[string]any{"name": "F"}, cookies)
	folderID := folder["id"].(string)

	_, log := postJSON(srv.URL+"/api/logs", map[string]any{"name": "L"}, cookies)
	logID := log["id"].(string)

	resp, _ := putJSON(srv.URL+"/api/logs/"+logID+"/placement", map[string]any{
		"folder_id": folderID,
		"position":  0,
	}, cookies)
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)

	_, body := getJSON(srv.URL+"/api/logs/"+logID, cookies)
	assert.Equal(t, folderID, body["folder_id"])
	assert.EqualValues(t, 0, body["position"])
}

func TestUpdateLogPlacement_ReorderWithinFolder(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()

	cookies := registerUser(t, srv.URL, "alice")

	_, a := postJSON(srv.URL+"/api/logs", map[string]any{"name": "A"}, cookies)
	_, b := postJSON(srv.URL+"/api/logs", map[string]any{"name": "B"}, cookies)
	_, c := postJSON(srv.URL+"/api/logs", map[string]any{"name": "C"}, cookies)

	// Move C to position 0 -> C, A, B
	resp, _ := putJSON(srv.URL+"/api/logs/"+c["id"].(string)+"/placement", map[string]any{
		"folder_id": nil,
		"position":  0,
	}, cookies)
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)

	_, body := getJSONArray(srv.URL+"/api/logs", cookies)
	require.Len(t, body, 3)
	assert.Equal(t, c["id"], body[0]["id"])
	assert.Equal(t, a["id"], body[1]["id"])
	assert.Equal(t, b["id"], body[2]["id"])
}

func TestUpdateLogPlacement_PerUserNotShared(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()

	aliceCookies := registerUser(t, srv.URL, "alice")
	bobCookies := registerUser(t, srv.URL, "bob")

	// Alice creates a log and shares it with Bob.
	_, log := postJSON(srv.URL+"/api/logs", map[string]any{"name": "Shared"}, aliceCookies)
	logID := log["id"].(string)

	_, shareBody := postJSON(srv.URL+"/api/logs/"+logID+"/share-token", map[string]any{}, aliceCookies)
	token := shareBody["share_token"].(string)
	postJSON(srv.URL+"/api/join/"+token, map[string]any{}, bobCookies)

	// Bob creates a folder and moves the shared log into it.
	_, folder := postJSON(srv.URL+"/api/folders", map[string]any{"name": "Bob's"}, bobCookies)
	folderID := folder["id"].(string)

	resp, _ := putJSON(srv.URL+"/api/logs/"+logID+"/placement", map[string]any{
		"folder_id": folderID,
		"position":  0,
	}, bobCookies)
	require.Equal(t, http.StatusNoContent, resp.StatusCode)

	// Bob sees the log in his folder.
	_, bobView := getJSON(srv.URL+"/api/logs/"+logID, bobCookies)
	assert.Equal(t, folderID, bobView["folder_id"])

	// Alice still sees the log at her root, unaffected.
	_, aliceView := getJSON(srv.URL+"/api/logs/"+logID, aliceCookies)
	assert.Nil(t, aliceView["folder_id"])
}

func TestUpdateLogPlacement_FolderBelongsToOtherUser(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()

	aliceCookies := registerUser(t, srv.URL, "alice")
	bobCookies := registerUser(t, srv.URL, "bob")

	_, aliceFolder := postJSON(srv.URL+"/api/folders", map[string]any{"name": "Alice"}, aliceCookies)
	aliceFolderID := aliceFolder["id"].(string)

	_, bobLog := postJSON(srv.URL+"/api/logs", map[string]any{"name": "Bob's"}, bobCookies)
	bobLogID := bobLog["id"].(string)

	resp, body := putJSON(srv.URL+"/api/logs/"+bobLogID+"/placement", map[string]any{
		"folder_id": aliceFolderID,
		"position":  0,
	}, bobCookies)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Contains(t, body["error"], "folder")
}

func TestUpdateLogPlacement_NoAccess(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()

	aliceCookies := registerUser(t, srv.URL, "alice")
	bobCookies := registerUser(t, srv.URL, "bob")

	_, log := postJSON(srv.URL+"/api/logs", map[string]any{"name": "L"}, aliceCookies)
	logID := log["id"].(string)

	resp, _ := putJSON(srv.URL+"/api/logs/"+logID+"/placement", map[string]any{
		"folder_id": nil,
		"position":  0,
	}, bobCookies)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestJoinLog_CreatesPlacementForJoiner(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()

	aliceCookies := registerUser(t, srv.URL, "alice")
	bobCookies := registerUser(t, srv.URL, "bob")

	_, log := postJSON(srv.URL+"/api/logs", map[string]any{"name": "Shared"}, aliceCookies)
	logID := log["id"].(string)

	_, shareBody := postJSON(srv.URL+"/api/logs/"+logID+"/share-token", map[string]any{}, aliceCookies)
	token := shareBody["share_token"].(string)
	postJSON(srv.URL+"/api/join/"+token, map[string]any{}, bobCookies)

	_, body := getJSONArray(srv.URL+"/api/logs", bobCookies)
	require.Len(t, body, 1)
	assert.Equal(t, logID, body[0]["id"])
	assert.Nil(t, body[0]["folder_id"])
	assert.EqualValues(t, 0, body[0]["position"])
	assert.Equal(t, false, body[0]["is_owner"])
}

// --- Pin to home + home position ---

func TestCreateLog_DefaultsPinnedWithHomePosition(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()

	cookies := registerUser(t, srv.URL, "alice")

	_, a := postJSON(srv.URL+"/api/logs", map[string]any{"name": "A"}, cookies)
	_, b := postJSON(srv.URL+"/api/logs", map[string]any{"name": "B"}, cookies)

	assert.Equal(t, true, a["pinned_to_home"])
	assert.EqualValues(t, 0, a["home_position"])
	assert.Equal(t, true, b["pinned_to_home"])
	assert.EqualValues(t, 1, b["home_position"])
}

func TestPinLog_UnpinAndRepin(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()

	cookies := registerUser(t, srv.URL, "alice")

	_, a := postJSON(srv.URL+"/api/logs", map[string]any{"name": "A"}, cookies)
	_, b := postJSON(srv.URL+"/api/logs", map[string]any{"name": "B"}, cookies)
	_, c := postJSON(srv.URL+"/api/logs", map[string]any{"name": "C"}, cookies)

	// Unpin B.
	resp, _ := putJSON(srv.URL+"/api/logs/"+b["id"].(string)+"/pin",
		map[string]any{"pinned": false}, cookies)
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)

	_, body := getJSON(srv.URL+"/api/logs/"+b["id"].(string), cookies)
	assert.Equal(t, false, body["pinned_to_home"])

	// A and C still pinned at their original home_positions (0 and 2).
	_, ja := getJSON(srv.URL+"/api/logs/"+a["id"].(string), cookies)
	_, jc := getJSON(srv.URL+"/api/logs/"+c["id"].(string), cookies)
	assert.EqualValues(t, 0, ja["home_position"])
	assert.EqualValues(t, 2, jc["home_position"])

	// Re-pin B: appended to end (home_position = max+1 = 3).
	resp, _ = putJSON(srv.URL+"/api/logs/"+b["id"].(string)+"/pin",
		map[string]any{"pinned": true}, cookies)
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)

	_, body = getJSON(srv.URL+"/api/logs/"+b["id"].(string), cookies)
	assert.Equal(t, true, body["pinned_to_home"])
	assert.EqualValues(t, 3, body["home_position"])
}

func TestPinLog_IdempotentNoChange(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()

	cookies := registerUser(t, srv.URL, "alice")
	_, a := postJSON(srv.URL+"/api/logs", map[string]any{"name": "A"}, cookies)

	// Already pinned. Pinning again is a no-op (home_position stays 0).
	resp, _ := putJSON(srv.URL+"/api/logs/"+a["id"].(string)+"/pin",
		map[string]any{"pinned": true}, cookies)
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)

	_, body := getJSON(srv.URL+"/api/logs/"+a["id"].(string), cookies)
	assert.EqualValues(t, 0, body["home_position"])
}

func TestPinLog_PerUserNotShared(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()

	aliceCookies := registerUser(t, srv.URL, "alice")
	bobCookies := registerUser(t, srv.URL, "bob")

	_, log := postJSON(srv.URL+"/api/logs", map[string]any{"name": "Shared"}, aliceCookies)
	logID := log["id"].(string)
	_, shareBody := postJSON(srv.URL+"/api/logs/"+logID+"/share-token", map[string]any{}, aliceCookies)
	token := shareBody["share_token"].(string)
	postJSON(srv.URL+"/api/join/"+token, map[string]any{}, bobCookies)

	// Bob unpins for himself. Alice's pin state stays true.
	resp, _ := putJSON(srv.URL+"/api/logs/"+logID+"/pin",
		map[string]any{"pinned": false}, bobCookies)
	require.Equal(t, http.StatusNoContent, resp.StatusCode)

	_, bobView := getJSON(srv.URL+"/api/logs/"+logID, bobCookies)
	assert.Equal(t, false, bobView["pinned_to_home"])

	_, aliceView := getJSON(srv.URL+"/api/logs/"+logID, aliceCookies)
	assert.Equal(t, true, aliceView["pinned_to_home"])
}

func TestUpdateLogHomePosition_Reorder(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()

	cookies := registerUser(t, srv.URL, "alice")
	_, a := postJSON(srv.URL+"/api/logs", map[string]any{"name": "A"}, cookies)
	_, b := postJSON(srv.URL+"/api/logs", map[string]any{"name": "B"}, cookies)
	_, c := postJSON(srv.URL+"/api/logs", map[string]any{"name": "C"}, cookies)

	// Move C from position 2 to position 0 -> C, A, B.
	resp, _ := putJSON(srv.URL+"/api/logs/"+c["id"].(string)+"/home-position",
		map[string]any{"home_position": 0}, cookies)
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)

	_, ja := getJSON(srv.URL+"/api/logs/"+a["id"].(string), cookies)
	_, jb := getJSON(srv.URL+"/api/logs/"+b["id"].(string), cookies)
	_, jc := getJSON(srv.URL+"/api/logs/"+c["id"].(string), cookies)
	assert.EqualValues(t, 0, jc["home_position"])
	assert.EqualValues(t, 1, ja["home_position"])
	assert.EqualValues(t, 2, jb["home_position"])
}

func TestUpdateLogHomePosition_RejectsUnpinned(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()

	cookies := registerUser(t, srv.URL, "alice")
	_, a := postJSON(srv.URL+"/api/logs", map[string]any{"name": "A"}, cookies)
	logID := a["id"].(string)

	// Unpin it.
	resp, _ := putJSON(srv.URL+"/api/logs/"+logID+"/pin",
		map[string]any{"pinned": false}, cookies)
	require.Equal(t, http.StatusNoContent, resp.StatusCode)

	// Reordering an unpinned log returns 400.
	resp, body := putJSON(srv.URL+"/api/logs/"+logID+"/home-position",
		map[string]any{"home_position": 0}, cookies)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Contains(t, body["error"], "not pinned")
}

func TestUpdateLogHomePosition_NoAccess(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()

	aliceCookies := registerUser(t, srv.URL, "alice")
	bobCookies := registerUser(t, srv.URL, "bob")
	_, log := postJSON(srv.URL+"/api/logs", map[string]any{"name": "A"}, aliceCookies)

	resp, _ := putJSON(srv.URL+"/api/logs/"+log["id"].(string)+"/home-position",
		map[string]any{"home_position": 0}, bobCookies)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// Regression: removing a share leaves the user's placement row in place
// (there's no FK between log_shares and user_log_placements). A subsequent
// re-join must not 23505 on the placement PK.
func TestJoinLog_RejoinAfterShareRemoval(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()

	aliceCookies := registerUser(t, srv.URL, "alice")
	bobCookies := registerUser(t, srv.URL, "bob")

	_, log := postJSON(srv.URL+"/api/logs", map[string]any{"name": "Shared"}, aliceCookies)
	logID := log["id"].(string)

	_, shareBody := postJSON(srv.URL+"/api/logs/"+logID+"/share-token", map[string]any{}, aliceCookies)
	token := shareBody["share_token"].(string)

	// Bob joins, organizes the log into a folder, then Alice removes him.
	resp, _ := postJSON(srv.URL+"/api/join/"+token, map[string]any{}, bobCookies)
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	_, folder := postJSON(srv.URL+"/api/folders", map[string]any{"name": "Bob's"}, bobCookies)
	folderID := folder["id"].(string)
	resp, _ = putJSON(srv.URL+"/api/logs/"+logID+"/placement", map[string]any{
		"folder_id": folderID,
		"position":  0,
	}, bobCookies)
	require.Equal(t, http.StatusNoContent, resp.StatusCode)

	_, shares := getJSONArray(srv.URL+"/api/logs/"+logID+"/shares", aliceCookies)
	require.Len(t, shares, 1)
	shareID := shares[0]["id"].(string)
	resp, _ = deleteJSON(srv.URL+"/api/logs/"+logID+"/shares/"+shareID, aliceCookies)
	require.Equal(t, http.StatusNoContent, resp.StatusCode)

	// Bob re-joins — must succeed, must not 500.
	resp, _ = postJSON(srv.URL+"/api/join/"+token, map[string]any{}, bobCookies)
	require.Equal(t, http.StatusCreated, resp.StatusCode)

	// And the prior placement (folder, position) was preserved.
	_, body := getJSON(srv.URL+"/api/logs/"+logID, bobCookies)
	assert.Equal(t, folderID, body["folder_id"])
}

// A path segment that is not a UUID names nothing. It used to reach
// PostgreSQL, which rejected the cast and turned a bad request into a 500 with
// a database error in the log.
func TestMalformedIDsAreRejectedNotFatal(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()

	cookies := registerUser(t, srv.URL, "malformed")
	resp, log := postJSON(srv.URL+"/api/logs", map[string]any{"name": "Vitamins"}, cookies)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	logID := log["id"].(string)

	cases := []struct {
		method string
		url    string
		body   map[string]any
	}{
		{"GET", "/api/logs/not-a-uuid", nil},
		{"PUT", "/api/logs/not-a-uuid", map[string]any{"name": "Renamed"}},
		{"DELETE", "/api/logs/not-a-uuid", nil},
		{"GET", "/api/logs/not-a-uuid/entries", nil},
		{"POST", "/api/logs/not-a-uuid/entries", map[string]any{}},
		{"DELETE", "/api/logs/" + logID + "/entries/not-a-uuid", nil},
		{"PUT", "/api/logs/not-a-uuid/placement", map[string]any{"position": 0}},
		{"POST", "/api/logs/not-a-uuid/share-token", nil},
		{"GET", "/api/logs/not-a-uuid/shares", nil},
	}
	for _, c := range cases {
		var resp *http.Response
		switch c.method {
		case "GET":
			resp, _ = getJSON(srv.URL+c.url, cookies)
		case "PUT":
			resp, _ = putJSON(srv.URL+c.url, c.body, cookies)
		case "POST":
			resp, _ = postJSON(srv.URL+c.url, c.body, cookies)
		case "DELETE":
			resp, _ = deleteJSON(srv.URL+c.url, cookies)
		}
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode, "%s %s", c.method, c.url)
	}
}

// Removing someone from a shared log used to hide it from their list without
// taking away their access: every single-log read scoped itself to the
// placement row, and that row deliberately survives a removal so a rejoin can
// restore the folder the log was filed under. The log stayed readable and
// writable by ID.
func TestRemoveShare_RevokesAccessNotJustTheListing(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()

	aliceCookies := registerUser(t, srv.URL, "alice")
	bobCookies := registerUser(t, srv.URL, "bob")

	_, log := postJSON(srv.URL+"/api/logs", map[string]any{"name": "Shared"}, aliceCookies)
	logID := log["id"].(string)
	_, shareBody := postJSON(srv.URL+"/api/logs/"+logID+"/share-token", map[string]any{}, aliceCookies)
	token := shareBody["share_token"].(string)

	resp, _ := postJSON(srv.URL+"/api/join/"+token, map[string]any{}, bobCookies)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	resp, entry := postJSON(srv.URL+"/api/logs/"+logID+"/entries", map[string]any{}, bobCookies)
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	entryID := entry["id"].(string)

	_, shares := getJSONArray(srv.URL+"/api/logs/"+logID+"/shares", aliceCookies)
	require.Len(t, shares, 1)
	resp, _ = deleteJSON(srv.URL+"/api/logs/"+logID+"/shares/"+shares[0]["id"].(string), aliceCookies)
	require.Equal(t, http.StatusNoContent, resp.StatusCode)

	// Bob is out. Every route that names the log directly must say so.
	resp, _ = getJSON(srv.URL+"/api/logs/"+logID, bobCookies)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode, "GET log")
	resp, _ = getJSONArray(srv.URL+"/api/logs/"+logID+"/entries", bobCookies)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode, "GET entries")
	resp, _ = postJSON(srv.URL+"/api/logs/"+logID+"/entries", map[string]any{}, bobCookies)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode, "POST entry")
	resp, _ = putJSON(srv.URL+"/api/logs/"+logID+"/entries/"+entryID, map[string]any{
		"fields":      map[string]any{},
		"occurred_at": time.Now().Format(time.RFC3339),
	}, bobCookies)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode, "PUT entry")
	resp, _ = deleteJSON(srv.URL+"/api/logs/"+logID+"/entries/"+entryID, bobCookies)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode, "DELETE entry")

	// Alice keeps the log and the entry Bob wrote while he was a member.
	resp, _ = getJSON(srv.URL+"/api/logs/"+logID, aliceCookies)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	_, entries := getJSONArray(srv.URL+"/api/logs/"+logID+"/entries", aliceCookies)
	assert.Len(t, entries, 1)
}
