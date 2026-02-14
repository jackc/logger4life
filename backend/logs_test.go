package backend

import (
	"encoding/json"
	"net/http"
	"testing"

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
	srv := setupTestRouter(t)
	defer srv.Close()

	cookies := registerUser(t, srv.URL, "alice")

	postJSON(srv.URL+"/api/logs", map[string]any{"name": "Vitamins"}, cookies)

	resp, body := postJSON(srv.URL+"/api/logs", map[string]any{"name": "Vitamins"}, cookies)

	assert.Equal(t, http.StatusConflict, resp.StatusCode)
	assert.Contains(t, body["error"], "already exists")
}

func TestCreateLog_WithFields(t *testing.T) {
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
	srv := setupTestRouter(t)
	defer srv.Close()

	resp, _ := postJSON(srv.URL+"/api/logs", map[string]any{"name": "Vitamins"}, nil)

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

// --- List Logs ---

func TestListLogs_Empty(t *testing.T) {
	srv := setupTestRouter(t)
	defer srv.Close()

	cookies := registerUser(t, srv.URL, "alice")

	resp, body := getJSONArray(srv.URL+"/api/logs", cookies)

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Len(t, body, 0)
}

func TestListLogs_ReturnUserLogs(t *testing.T) {
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
	srv := setupTestRouter(t)
	defer srv.Close()

	cookies := registerUser(t, srv.URL, "alice")

	resp, body := getJSON(srv.URL+"/api/logs/00000000-0000-0000-0000-000000000000", cookies)

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, "log not found", body["error"])
}

func TestGetLog_OtherUsersLog(t *testing.T) {
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
	srv := setupTestRouter(t)
	defer srv.Close()

	resp, _ := putJSON(srv.URL+"/api/logs/00000000-0000-0000-0000-000000000000", map[string]any{
		"name": "Test",
	}, nil)

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestUpdateLog_ExistingEntriesUntouched(t *testing.T) {
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
	srv := setupTestRouter(t)
	defer srv.Close()

	cookies := registerUser(t, srv.URL, "alice")

	_, created := postJSON(srv.URL+"/api/logs", map[string]any{"name": "Vitamins"}, cookies)
	logID := created["id"].(string)

	resp, body := postJSON(srv.URL+"/api/logs/"+logID+"/entries", map[string]any{}, cookies)

	assert.Equal(t, http.StatusCreated, resp.StatusCode)
	assert.NotEmpty(t, body["id"])
	assert.Equal(t, logID, body["log_id"])
	assert.NotEmpty(t, body["occurred_at"])
	assert.NotEmpty(t, body["created_at"])
	assert.NotEmpty(t, body["updated_at"])
}

func TestCreateLogEntry_NonexistentLog(t *testing.T) {
	srv := setupTestRouter(t)
	defer srv.Close()

	cookies := registerUser(t, srv.URL, "alice")

	resp, body := postJSON(srv.URL+"/api/logs/00000000-0000-0000-0000-000000000000/entries", map[string]any{}, cookies)

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, "log not found", body["error"])
}

func TestCreateLogEntry_OtherUsersLog(t *testing.T) {
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

	fields := body["fields"].(map[string]any)
	assert.Equal(t, "25", fields["count"])
	assert.Equal(t, "morning set", fields["notes"])
}

func TestCreateLogEntry_WrongFieldType(t *testing.T) {
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
}

func TestListLogEntries_OtherUsersLog(t *testing.T) {
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
	assert.Contains(t, body["occurred_at"], "2025-06-15T10:30:00")
	assert.NotEmpty(t, body["created_at"])
	assert.NotEmpty(t, body["updated_at"])
}

func TestUpdateLogEntry_WithFields(t *testing.T) {
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
	fields := body["fields"].(map[string]any)
	assert.Equal(t, "30", fields["count"])
	assert.Equal(t, "evening set", fields["notes"])
}

func TestUpdateLogEntry_NonexistentEntry(t *testing.T) {
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
	srv := setupTestRouter(t)
	defer srv.Close()

	cookies := registerUser(t, srv.URL, "alice")

	resp, body := deleteJSON(srv.URL+"/api/logs/00000000-0000-0000-0000-000000000000/entries/00000000-0000-0000-0000-000000000000", cookies)

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, "log not found", body["error"])
}

func TestDeleteLogEntry_OtherUsersLog(t *testing.T) {
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
	srv := setupTestRouter(t)
	defer srv.Close()

	resp, _ := deleteJSON(srv.URL+"/api/logs/00000000-0000-0000-0000-000000000000/entries/00000000-0000-0000-0000-000000000000", nil)

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

// --- Delete Log ---

func TestDeleteLog_Success(t *testing.T) {
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
	srv := setupTestRouter(t)
	defer srv.Close()

	cookies := registerUser(t, srv.URL, "alice")

	resp, body := deleteJSON(srv.URL+"/api/logs/00000000-0000-0000-0000-000000000000", cookies)

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, "log not found", body["error"])
}

func TestDeleteLog_OtherUsersLog(t *testing.T) {
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
	srv := setupTestRouter(t)
	defer srv.Close()

	resp, _ := deleteJSON(srv.URL+"/api/logs/00000000-0000-0000-0000-000000000000", nil)

	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}
