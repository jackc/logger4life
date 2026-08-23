package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/logger4life/backend/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sqlExec posts a query and returns the parsed response (columns/rows/etc).
func sqlExec(t *testing.T, srvURL, query string, cookies []*http.Cookie) (*http.Response, map[string]any) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"query": query})
	req, _ := http.NewRequest("POST", srvURL+"/api/sql/execute", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for _, c := range cookies {
		req.AddCookie(c)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	var result map[string]any
	json.NewDecoder(resp.Body).Decode(&result)
	resp.Body.Close()
	return resp, result
}

func createLogViaAPI(t *testing.T, srvURL, name string, cookies []*http.Cookie) string {
	t.Helper()
	resp, body := postJSON(srvURL+"/api/logs", map[string]any{
		"name":   name,
		"fields": []map[string]any{{"name": "count", "type": "number", "required": false}},
	}, cookies)
	require.Equal(t, http.StatusCreated, resp.StatusCode, "create log failed: %v", body)
	return body["id"].(string)
}

func createLogEntryViaAPI(t *testing.T, srvURL, logID string, count string, cookies []*http.Cookie) {
	t.Helper()
	resp, body := postJSON(srvURL+"/api/logs/"+logID+"/entries", map[string]any{
		"fields": map[string]any{"count": count},
	}, cookies)
	require.Equal(t, http.StatusCreated, resp.StatusCode, "create entry failed: %v", body)
}

func shareLogToUserViaAPI(t *testing.T, srvURL, logID, otherUsername string, ownerCookies, otherCookies []*http.Cookie) {
	t.Helper()
	tokenResp, tokenBody := postJSON(srvURL+"/api/logs/"+logID+"/share-token", nil, ownerCookies)
	require.Equal(t, http.StatusOK, tokenResp.StatusCode, "share-token failed: %v", tokenBody)
	token := tokenBody["share_token"].(string)
	joinResp, joinBody := postJSON(srvURL+"/api/join/"+token, nil, otherCookies)
	require.Equal(t, http.StatusCreated, joinResp.StatusCode, "join failed: %v", joinBody)
}

// ---------------------------------------------------------------------------
// Execute
// ---------------------------------------------------------------------------

func TestSQLExecute_SelectFromLogs(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()

	cookies := registerUser(t, srv.URL, "alice")
	createLogViaAPI(t, srv.URL, "Vitamins", cookies)
	createLogViaAPI(t, srv.URL, "Pushups", cookies)

	resp, body := sqlExec(t, srv.URL, "SELECT name FROM logs ORDER BY name", cookies)
	require.Equal(t, http.StatusOK, resp.StatusCode, "%v", body)

	rows := body["rows"].([]any)
	assert.Len(t, rows, 2)
	assert.Equal(t, "Pushups", rows[0].([]any)[0])
	assert.Equal(t, "Vitamins", rows[1].([]any)[0])

	cols := body["columns"].([]any)
	assert.Len(t, cols, 1)
	assert.Equal(t, "name", cols[0].(map[string]any)["name"])
}

func TestSQLExecute_DataIsolation(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()

	aliceCookies := registerUser(t, srv.URL, "alice")
	bobCookies := registerUser(t, srv.URL, "bob")
	createLogViaAPI(t, srv.URL, "AlicesLog", aliceCookies)
	createLogViaAPI(t, srv.URL, "BobsLog", bobCookies)

	_, aliceBody := sqlExec(t, srv.URL, "SELECT name FROM logs", aliceCookies)
	aliceRows := aliceBody["rows"].([]any)
	require.Len(t, aliceRows, 1)
	assert.Equal(t, "AlicesLog", aliceRows[0].([]any)[0])

	_, bobBody := sqlExec(t, srv.URL, "SELECT name FROM logs", bobCookies)
	bobRows := bobBody["rows"].([]any)
	require.Len(t, bobRows, 1)
	assert.Equal(t, "BobsLog", bobRows[0].([]any)[0])
}

func TestSQLExecute_LogEntriesFiltered(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()

	aliceCookies := registerUser(t, srv.URL, "alice")
	bobCookies := registerUser(t, srv.URL, "bob")
	aliceLogID := createLogViaAPI(t, srv.URL, "AlicesLog", aliceCookies)
	bobLogID := createLogViaAPI(t, srv.URL, "BobsLog", bobCookies)
	createLogEntryViaAPI(t, srv.URL, aliceLogID, "1", aliceCookies)
	createLogEntryViaAPI(t, srv.URL, bobLogID, "2", bobCookies)

	_, body := sqlExec(t, srv.URL, "SELECT user_username FROM log_entries", aliceCookies)
	rows := body["rows"].([]any)
	require.Len(t, rows, 1)
	assert.Equal(t, "alice", rows[0].([]any)[0])
}

func TestSQLExecute_SharedWith_OwnerSeesUsernames(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()

	aliceCookies := registerUser(t, srv.URL, "alice")
	bobCookies := registerUser(t, srv.URL, "bob")
	logID := createLogViaAPI(t, srv.URL, "Shared", aliceCookies)
	shareLogToUserViaAPI(t, srv.URL, logID, "bob", aliceCookies, bobCookies)

	// Owner sees the shared_with array populated with the sharee's username.
	_, ownerBody := sqlExec(t, srv.URL, "SELECT shared_with FROM logs", aliceCookies)
	ownerRows := ownerBody["rows"].([]any)
	require.Len(t, ownerRows, 1)
	sharedWith := ownerRows[0].([]any)[0].(string)
	assert.Equal(t, "{bob}", sharedWith)

	// Sharee sees NULL.
	_, shareeBody := sqlExec(t, srv.URL, "SELECT shared_with FROM logs", bobCookies)
	shareeRows := shareeBody["rows"].([]any)
	require.Len(t, shareeRows, 1)
	assert.Nil(t, shareeRows[0].([]any)[0])
}

func TestSQLExecute_RejectsDDL(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()
	cookies := registerUser(t, srv.URL, "alice")

	for _, q := range []string{
		"CREATE TABLE evil (id int)",
		"DROP TABLE logs",
		"ALTER TABLE logs ADD COLUMN evil int",
		"TRUNCATE logs",
	} {
		resp, body := sqlExec(t, srv.URL, q, cookies)
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode, "expected reject for: %s (got %v)", q, body)
	}
}

func TestSQLExecute_RejectsWrites(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()
	cookies := registerUser(t, srv.URL, "alice")

	for _, q := range []string{
		"INSERT INTO logs (name) VALUES ('x')",
		"UPDATE logs SET name = 'x'",
		"DELETE FROM logs",
	} {
		resp, body := sqlExec(t, srv.URL, q, cookies)
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode, "expected reject for: %s (got %v)", q, body)
	}
}

func TestSQLExecute_RejectsPublicSchema(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()
	cookies := registerUser(t, srv.URL, "alice")

	resp, body := sqlExec(t, srv.URL, "SELECT * FROM public.users", cookies)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode, "%v", body)
	assert.Contains(t, body["error"], "public.users")
}

func TestSQLExecute_RejectsSchemaQualifiedReferences(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()
	cookies := registerUser(t, srv.URL, "alice")

	// Users should always reference views unqualified — search_path resolves them.
	resp, body := sqlExec(t, srv.URL, "SELECT * FROM sql_query.logs", cookies)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode, "%v", body)
	assert.Contains(t, body["error"], "sql_query.logs")
}

func TestSQLExecute_RejectsDangerousFunctions(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()
	cookies := registerUser(t, srv.URL, "alice")

	for _, q := range []string{
		"SELECT pg_sleep(10)",
		"SELECT set_config('statement_timeout', '0', false)",
		"SELECT lo_import('/etc/passwd')",
	} {
		resp, body := sqlExec(t, srv.URL, q, cookies)
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode, "expected reject for: %s (got %v)", q, body)
	}
}

func TestSQLExecute_RejectsMultiStatement(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()
	cookies := registerUser(t, srv.URL, "alice")

	resp, body := sqlExec(t, srv.URL, "SELECT 1; SELECT 2", cookies)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode, "%v", body)
}

func TestSQLExecute_RejectsEmptyQuery(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()
	cookies := registerUser(t, srv.URL, "alice")

	resp, body := sqlExec(t, srv.URL, "   ", cookies)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Contains(t, body["error"], "required")
}

func TestSQLExecute_Unauthenticated(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()

	resp, _ := sqlExec(t, srv.URL, "SELECT 1", nil)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestSQLExecute_TruncatesAt1000Rows(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()
	cookies := registerUser(t, srv.URL, "alice")

	// generate_series is in the default allowed function list.
	resp, body := sqlExec(t, srv.URL,
		"SELECT g FROM generate_series(1, 1500) g", cookies)
	require.Equal(t, http.StatusOK, resp.StatusCode, "%v", body)
	assert.Equal(t, true, body["truncated"])
	assert.Equal(t, float64(1000), body["row_count"])
}

func TestSQLExecute_TruncatesAtResultByteLimit(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()
	cookies := registerUser(t, srv.URL, "alice")

	resp, body := sqlExec(t, srv.URL,
		"SELECT CASE WHEN g = 1 THEN 'kept' ELSE repeat('x', 1048576) END AS value FROM generate_series(1, 2) g ORDER BY g",
		cookies,
	)
	require.Equal(t, http.StatusOK, resp.StatusCode, "%v", body)
	assert.Equal(t, true, body["truncated"])
	assert.Equal(t, float64(1), body["row_count"])
	rows := body["rows"].([]any)
	require.Len(t, rows, 1)
	assert.Equal(t, "kept", rows[0].([]any)[0])
}

func TestSQLExecute_RedactsPostgresErrorDetails(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()
	cookies := registerUser(t, srv.URL, "alice")

	resp, body := sqlExec(t, srv.URL, "SELECT 'secret-value'::integer", cookies)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode, "%v", body)
	message := body["error"].(string)
	assert.Equal(t, "query failed (SQLSTATE 22P02)", message)
	assert.NotContains(t, message, "secret-value")
}

func TestSQLExecute_CollapsesParserDetails(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()
	cookies := registerUser(t, srv.URL, "alice")

	resp, body := sqlExec(t, srv.URL, "SELECT (", cookies)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode, "%v", body)
	assert.Equal(t, "invalid SQL query", body["error"])
}

func TestSQLExecute_StatementTimeout(t *testing.T) {
	t.Parallel()
	if testBackend() != "postgresql" {
		t.Skip("the billion-row cancellation probe is PostgreSQL-specific")
	}
	srv := setupTestRouter(t)
	defer srv.Close()
	cookies := registerUser(t, srv.URL, "alice")

	// 100M generate_series in COUNT will take much longer than 5s.
	resp, body := sqlExec(t, srv.URL,
		"SELECT count(*) FROM generate_series(1, 1000000000) g", cookies)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode, "%v", body)
	assert.Contains(t, strings.ToLower(body["error"].(string)), "timed out")
}

type failingUserSQLExecutor struct{ err error }

func (e failingUserSQLExecutor) ExecuteUserSQL(context.Context, string, string) (core.UserSQLResult, error) {
	return core.UserSQLResult{}, e.err
}

func TestSQLExecute_DoesNotExposeInternalPortErrors(t *testing.T) {
	t.Parallel()
	secret := "database host is secret.internal"
	app := core.New(core.Config{UserSQL: failingUserSQLExecutor{err: errors.New(secret)}})
	handler := handleExecuteSQL(app)
	req := httptest.NewRequest(http.MethodPost, "/api/sql/execute", strings.NewReader(`{"query":"SELECT 1"}`))
	user := &AuthUser{ID: "user-1", Username: "alice"}
	req = req.WithContext(context.WithValue(req.Context(), userContextKey, user))
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)
	assert.Equal(t, http.StatusInternalServerError, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "internal error")
	assert.NotContains(t, recorder.Body.String(), secret)
}

// ---------------------------------------------------------------------------
// Schema description
// ---------------------------------------------------------------------------

func TestSQLSchema_ReturnsViewsWithComments(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()
	cookies := registerUser(t, srv.URL, "alice")

	resp, body := getJSON(srv.URL+"/api/sql/schema", cookies)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	views := body["views"].([]any)
	require.Len(t, views, 2)

	names := []string{}
	for _, v := range views {
		names = append(names, v.(map[string]any)["name"].(string))
	}
	assert.ElementsMatch(t, []string{"logs", "log_entries"}, names)

	// Find logs view and verify a column has its comment.
	for _, v := range views {
		vm := v.(map[string]any)
		if vm["name"] != "logs" {
			continue
		}
		assert.Contains(t, vm["comment"], "own")
		cols := vm["columns"].([]any)
		var sharedWithComment string
		for _, c := range cols {
			cm := c.(map[string]any)
			if cm["name"] == "shared_with" {
				sharedWithComment = cm["comment"].(string)
				assert.Equal(t, "text[]", cm["data_type"])
			}
		}
		assert.Contains(t, sharedWithComment, "owner")
	}
}

// ---------------------------------------------------------------------------
// Saved queries
// ---------------------------------------------------------------------------

func TestSavedQueries_CRUD(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()
	cookies := registerUser(t, srv.URL, "alice")

	// Create.
	createResp, createBody := postJSON(srv.URL+"/api/sql/saved", map[string]any{
		"name":       "All logs",
		"query_text": "SELECT * FROM logs",
	}, cookies)
	require.Equal(t, http.StatusCreated, createResp.StatusCode)
	queryID := createBody["id"].(string)
	assert.Equal(t, "All logs", createBody["name"])

	// List.
	listResp, _ := http.Get(srv.URL + "/api/sql/saved") // unauth → 401
	listResp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, listResp.StatusCode)

	listResp2, listArr := getJSONArray(srv.URL+"/api/sql/saved", cookies)
	require.Equal(t, http.StatusOK, listResp2.StatusCode)
	require.Len(t, listArr, 1)
	assert.Equal(t, "All logs", listArr[0]["name"])

	// Update.
	updResp, updBody := putJSON(srv.URL+"/api/sql/saved/"+queryID, map[string]any{
		"name":       "All logs renamed",
		"query_text": "SELECT id FROM logs",
	}, cookies)
	require.Equal(t, http.StatusOK, updResp.StatusCode)
	assert.Equal(t, "All logs renamed", updBody["name"])
	assert.Equal(t, "SELECT id FROM logs", updBody["query_text"])

	// Delete.
	delResp, _ := deleteJSON(srv.URL+"/api/sql/saved/"+queryID, cookies)
	assert.Equal(t, http.StatusNoContent, delResp.StatusCode)

	_, listAfter := getJSONArray(srv.URL+"/api/sql/saved", cookies)
	assert.Len(t, listAfter, 0)
}

func TestSavedQueries_DuplicateName(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()
	cookies := registerUser(t, srv.URL, "alice")

	postJSON(srv.URL+"/api/sql/saved", map[string]any{
		"name": "x", "query_text": "SELECT 1",
	}, cookies)

	resp, body := postJSON(srv.URL+"/api/sql/saved", map[string]any{
		"name": "x", "query_text": "SELECT 2",
	}, cookies)
	assert.Equal(t, http.StatusConflict, resp.StatusCode)
	assert.Contains(t, body["error"], "already exists")
}

func TestSavedQueries_Isolation(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()

	aliceCookies := registerUser(t, srv.URL, "alice")
	bobCookies := registerUser(t, srv.URL, "bob")

	createResp, createBody := postJSON(srv.URL+"/api/sql/saved", map[string]any{
		"name": "alice's", "query_text": "SELECT 1",
	}, aliceCookies)
	require.Equal(t, http.StatusCreated, createResp.StatusCode)
	queryID := createBody["id"].(string)

	// Bob's list does not include Alice's.
	_, bobList := getJSONArray(srv.URL+"/api/sql/saved", bobCookies)
	assert.Len(t, bobList, 0)

	// Bob cannot update Alice's query.
	updResp, _ := putJSON(srv.URL+"/api/sql/saved/"+queryID, map[string]any{
		"name": "hacked", "query_text": "SELECT 99",
	}, bobCookies)
	assert.Equal(t, http.StatusNotFound, updResp.StatusCode)

	// Bob cannot delete Alice's query.
	delResp, _ := deleteJSON(srv.URL+"/api/sql/saved/"+queryID, bobCookies)
	assert.Equal(t, http.StatusNotFound, delResp.StatusCode)

	// Alice's query is still there.
	_, aliceList := getJSONArray(srv.URL+"/api/sql/saved", aliceCookies)
	require.Len(t, aliceList, 1)
	assert.Equal(t, "alice's", aliceList[0]["name"])
}

func TestSavedQueries_ValidationErrors(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()
	cookies := registerUser(t, srv.URL, "alice")

	resp1, body1 := postJSON(srv.URL+"/api/sql/saved", map[string]any{
		"name": "", "query_text": "SELECT 1",
	}, cookies)
	assert.Equal(t, http.StatusBadRequest, resp1.StatusCode)
	assert.Contains(t, body1["error"], "name")

	resp2, body2 := postJSON(srv.URL+"/api/sql/saved", map[string]any{
		"name": "valid", "query_text": "",
	}, cookies)
	assert.Equal(t, http.StatusBadRequest, resp2.StatusCode)
	assert.Contains(t, body2["error"], "query_text")
}

// ---------------------------------------------------------------------------
// Name lookup (used by the MCP run_saved_query tool)
// ---------------------------------------------------------------------------

func TestGetSavedQueryByName(t *testing.T) {
	t.Parallel()
	srv := setupTestRouter(t)
	defer srv.Close()
	cookies := registerUser(t, srv.URL, "alice")

	meResp, meBody := getJSON(srv.URL+"/api/me", cookies)
	require.Equal(t, http.StatusOK, meResp.StatusCode)
	userID := meBody["id"].(string)

	_, createBody := postJSON(srv.URL+"/api/sql/saved", map[string]any{
		"name":       "log count",
		"query_text": "SELECT count(*) FROM logs",
	}, cookies)
	wantID := createBody["id"].(string)

	app := srv.app

	q, err := core.GetSavedQuery.Call(core.WithUserID(context.Background(), userID), app,
		core.GetSavedQueryParams{Name: "log count"})
	require.NoError(t, err)
	assert.Equal(t, wantID, q.ID)
	assert.Equal(t, "log count", q.Name)
	assert.Equal(t, "SELECT count(*) FROM logs", q.QueryText)

	// A missing row must surface as the core sentinel, not a pgx error.
	_, err = core.GetSavedQuery.Call(core.WithUserID(context.Background(), userID), app,
		core.GetSavedQueryParams{Name: "nope"})
	require.Error(t, err)
	assert.True(t, errors.Is(err, core.ErrSavedQueryNotFound), "expected ErrSavedQueryNotFound, got %v", err)

	// Different user with the same query name must not be visible.
	otherCookies := registerUser(t, srv.URL, "bob")
	meResp2, meBody2 := getJSON(srv.URL+"/api/me", otherCookies)
	require.Equal(t, http.StatusOK, meResp2.StatusCode)
	bobID := meBody2["id"].(string)
	_, err = core.GetSavedQuery.Call(core.WithUserID(context.Background(), bobID), app,
		core.GetSavedQueryParams{Name: "log count"})
	require.Error(t, err)
	assert.True(t, errors.Is(err, core.ErrSavedQueryNotFound))
}
