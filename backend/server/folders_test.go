package server

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Create Folder ---

func TestCreateFolder_AtRoot(t *testing.T) {
	srv := setupTestRouter(t)
	defer srv.Close()

	cookies := registerUser(t, srv.URL, "alice")

	resp, body := postJSON(srv.URL+"/api/folders", map[string]any{"name": "Health"}, cookies)

	assert.Equal(t, http.StatusCreated, resp.StatusCode)
	assert.Equal(t, "Health", body["name"])
	assert.Nil(t, body["parent_folder_id"])
	assert.EqualValues(t, 0, body["position"])
	assert.NotEmpty(t, body["id"])
}

func TestCreateFolder_AppendsToEnd(t *testing.T) {
	srv := setupTestRouter(t)
	defer srv.Close()

	cookies := registerUser(t, srv.URL, "alice")

	_, a := postJSON(srv.URL+"/api/folders", map[string]any{"name": "A"}, cookies)
	_, b := postJSON(srv.URL+"/api/folders", map[string]any{"name": "B"}, cookies)
	_, c := postJSON(srv.URL+"/api/folders", map[string]any{"name": "C"}, cookies)

	assert.EqualValues(t, 0, a["position"])
	assert.EqualValues(t, 1, b["position"])
	assert.EqualValues(t, 2, c["position"])
}

func TestCreateFolder_Nested(t *testing.T) {
	srv := setupTestRouter(t)
	defer srv.Close()

	cookies := registerUser(t, srv.URL, "alice")

	_, parent := postJSON(srv.URL+"/api/folders", map[string]any{"name": "Parent"}, cookies)
	parentID := parent["id"].(string)

	resp, body := postJSON(srv.URL+"/api/folders", map[string]any{
		"name":             "Child",
		"parent_folder_id": parentID,
	}, cookies)

	assert.Equal(t, http.StatusCreated, resp.StatusCode)
	assert.Equal(t, "Child", body["name"])
	assert.Equal(t, parentID, body["parent_folder_id"])
	assert.EqualValues(t, 0, body["position"])
}

func TestCreateFolder_EmptyName(t *testing.T) {
	srv := setupTestRouter(t)
	defer srv.Close()

	cookies := registerUser(t, srv.URL, "alice")

	resp, body := postJSON(srv.URL+"/api/folders", map[string]any{"name": ""}, cookies)

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Contains(t, body["error"], "name")
}

func TestCreateFolder_ParentBelongsToOtherUser(t *testing.T) {
	srv := setupTestRouter(t)
	defer srv.Close()

	aliceCookies := registerUser(t, srv.URL, "alice")
	bobCookies := registerUser(t, srv.URL, "bob")

	_, aliceFolder := postJSON(srv.URL+"/api/folders", map[string]any{"name": "Alice"}, aliceCookies)
	aliceFolderID := aliceFolder["id"].(string)

	resp, body := postJSON(srv.URL+"/api/folders", map[string]any{
		"name":             "Sneaky",
		"parent_folder_id": aliceFolderID,
	}, bobCookies)

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Contains(t, body["error"], "parent")
}

// --- List Folders ---

func TestListFolders_Empty(t *testing.T) {
	srv := setupTestRouter(t)
	defer srv.Close()

	cookies := registerUser(t, srv.URL, "alice")

	resp, body := getJSONArray(srv.URL+"/api/folders", cookies)

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Len(t, body, 0)
}

func TestListFolders_ReturnsOnlyOwn(t *testing.T) {
	srv := setupTestRouter(t)
	defer srv.Close()

	aliceCookies := registerUser(t, srv.URL, "alice")
	bobCookies := registerUser(t, srv.URL, "bob")

	postJSON(srv.URL+"/api/folders", map[string]any{"name": "Alice Folder"}, aliceCookies)
	postJSON(srv.URL+"/api/folders", map[string]any{"name": "Bob Folder"}, bobCookies)

	_, body := getJSONArray(srv.URL+"/api/folders", aliceCookies)

	require.Len(t, body, 1)
	assert.Equal(t, "Alice Folder", body[0]["name"])
}

// --- Rename Folder ---

func TestRenameFolder_Success(t *testing.T) {
	srv := setupTestRouter(t)
	defer srv.Close()

	cookies := registerUser(t, srv.URL, "alice")

	_, created := postJSON(srv.URL+"/api/folders", map[string]any{"name": "Old"}, cookies)
	folderID := created["id"].(string)

	resp, body := putJSON(srv.URL+"/api/folders/"+folderID, map[string]any{"name": "New"}, cookies)

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "New", body["name"])
}

func TestRenameFolder_OtherUsersFolder(t *testing.T) {
	srv := setupTestRouter(t)
	defer srv.Close()

	aliceCookies := registerUser(t, srv.URL, "alice")
	bobCookies := registerUser(t, srv.URL, "bob")

	_, created := postJSON(srv.URL+"/api/folders", map[string]any{"name": "Alice"}, aliceCookies)
	folderID := created["id"].(string)

	resp, _ := putJSON(srv.URL+"/api/folders/"+folderID, map[string]any{"name": "Stolen"}, bobCookies)

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// --- Move Folder ---

func listFolders(t *testing.T, srvURL string, cookies []*http.Cookie) []map[string]any {
	t.Helper()
	_, body := getJSONArray(srvURL+"/api/folders", cookies)
	return body
}

func TestMoveFolder_WithinSameParent(t *testing.T) {
	srv := setupTestRouter(t)
	defer srv.Close()

	cookies := registerUser(t, srv.URL, "alice")

	_, a := postJSON(srv.URL+"/api/folders", map[string]any{"name": "A"}, cookies)
	_, b := postJSON(srv.URL+"/api/folders", map[string]any{"name": "B"}, cookies)
	_, c := postJSON(srv.URL+"/api/folders", map[string]any{"name": "C"}, cookies)

	// Move C to position 0 (C, A, B).
	cID := c["id"].(string)
	resp, _ := putJSON(srv.URL+"/api/folders/"+cID+"/move", map[string]any{
		"parent_folder_id": nil,
		"position":         0,
	}, cookies)
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)

	folders := listFolders(t, srv.URL, cookies)
	byID := map[string]map[string]any{}
	for _, f := range folders {
		byID[f["id"].(string)] = f
	}
	assert.EqualValues(t, 0, byID[cID]["position"])
	assert.EqualValues(t, 1, byID[a["id"].(string)]["position"])
	assert.EqualValues(t, 2, byID[b["id"].(string)]["position"])
}

func TestMoveFolder_ToDifferentParent(t *testing.T) {
	srv := setupTestRouter(t)
	defer srv.Close()

	cookies := registerUser(t, srv.URL, "alice")

	_, parent := postJSON(srv.URL+"/api/folders", map[string]any{"name": "Parent"}, cookies)
	parentID := parent["id"].(string)

	_, rootA := postJSON(srv.URL+"/api/folders", map[string]any{"name": "RootA"}, cookies)
	_, rootB := postJSON(srv.URL+"/api/folders", map[string]any{"name": "RootB"}, cookies)
	rootAID := rootA["id"].(string)
	rootBID := rootB["id"].(string)

	resp, _ := putJSON(srv.URL+"/api/folders/"+rootAID+"/move", map[string]any{
		"parent_folder_id": parentID,
		"position":         0,
	}, cookies)
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)

	folders := listFolders(t, srv.URL, cookies)
	byID := map[string]map[string]any{}
	for _, f := range folders {
		byID[f["id"].(string)] = f
	}
	// rootA now nested under parent at position 0.
	assert.Equal(t, parentID, byID[rootAID]["parent_folder_id"])
	assert.EqualValues(t, 0, byID[rootAID]["position"])
	// rootB shifted up from position 2 to 1 at the root.
	assert.Nil(t, byID[rootBID]["parent_folder_id"])
	assert.EqualValues(t, 1, byID[rootBID]["position"])
}

func TestMoveFolder_CycleRejected(t *testing.T) {
	srv := setupTestRouter(t)
	defer srv.Close()

	cookies := registerUser(t, srv.URL, "alice")

	_, parent := postJSON(srv.URL+"/api/folders", map[string]any{"name": "P"}, cookies)
	parentID := parent["id"].(string)
	_, child := postJSON(srv.URL+"/api/folders", map[string]any{
		"name":             "C",
		"parent_folder_id": parentID,
	}, cookies)
	childID := child["id"].(string)

	// Try to move P under C — should fail.
	resp, body := putJSON(srv.URL+"/api/folders/"+parentID+"/move", map[string]any{
		"parent_folder_id": childID,
		"position":         0,
	}, cookies)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Contains(t, body["error"], "descendant")
}

func TestMoveFolder_IntoSelfRejected(t *testing.T) {
	srv := setupTestRouter(t)
	defer srv.Close()

	cookies := registerUser(t, srv.URL, "alice")

	_, f := postJSON(srv.URL+"/api/folders", map[string]any{"name": "F"}, cookies)
	fID := f["id"].(string)

	resp, body := putJSON(srv.URL+"/api/folders/"+fID+"/move", map[string]any{
		"parent_folder_id": fID,
		"position":         0,
	}, cookies)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Contains(t, body["error"], "own parent")
}

// --- Delete Folder ---

func TestDeleteFolder_Empty(t *testing.T) {
	srv := setupTestRouter(t)
	defer srv.Close()

	cookies := registerUser(t, srv.URL, "alice")

	_, a := postJSON(srv.URL+"/api/folders", map[string]any{"name": "A"}, cookies)
	_, b := postJSON(srv.URL+"/api/folders", map[string]any{"name": "B"}, cookies)
	_, c := postJSON(srv.URL+"/api/folders", map[string]any{"name": "C"}, cookies)

	resp, _ := deleteJSON(srv.URL+"/api/folders/"+b["id"].(string), cookies)
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)

	folders := listFolders(t, srv.URL, cookies)
	require.Len(t, folders, 2)
	byID := map[string]map[string]any{}
	for _, f := range folders {
		byID[f["id"].(string)] = f
	}
	assert.EqualValues(t, 0, byID[a["id"].(string)]["position"])
	assert.EqualValues(t, 1, byID[c["id"].(string)]["position"])
}

func TestDeleteFolder_NonEmpty_Subfolder(t *testing.T) {
	srv := setupTestRouter(t)
	defer srv.Close()

	cookies := registerUser(t, srv.URL, "alice")

	_, parent := postJSON(srv.URL+"/api/folders", map[string]any{"name": "P"}, cookies)
	parentID := parent["id"].(string)
	postJSON(srv.URL+"/api/folders", map[string]any{
		"name":             "C",
		"parent_folder_id": parentID,
	}, cookies)

	resp, body := deleteJSON(srv.URL+"/api/folders/"+parentID, cookies)
	assert.Equal(t, http.StatusConflict, resp.StatusCode)
	assert.Contains(t, body["error"], "not empty")
}

func TestDeleteFolder_NonEmpty_Log(t *testing.T) {
	srv := setupTestRouter(t)
	defer srv.Close()

	cookies := registerUser(t, srv.URL, "alice")

	_, folder := postJSON(srv.URL+"/api/folders", map[string]any{"name": "F"}, cookies)
	folderID := folder["id"].(string)
	_, log := postJSON(srv.URL+"/api/logs", map[string]any{"name": "L"}, cookies)
	logID := log["id"].(string)

	// Move the log into the folder.
	resp, _ := putJSON(srv.URL+"/api/logs/"+logID+"/placement", map[string]any{
		"folder_id": folderID,
		"position":  0,
	}, cookies)
	require.Equal(t, http.StatusNoContent, resp.StatusCode)

	resp, body := deleteJSON(srv.URL+"/api/folders/"+folderID, cookies)
	assert.Equal(t, http.StatusConflict, resp.StatusCode)
	assert.Contains(t, body["error"], "not empty")
}

func TestDeleteFolder_OtherUsersFolder(t *testing.T) {
	srv := setupTestRouter(t)
	defer srv.Close()

	aliceCookies := registerUser(t, srv.URL, "alice")
	bobCookies := registerUser(t, srv.URL, "bob")

	_, created := postJSON(srv.URL+"/api/folders", map[string]any{"name": "Alice"}, aliceCookies)
	folderID := created["id"].(string)

	resp, _ := deleteJSON(srv.URL+"/api/folders/"+folderID, bobCookies)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// --- Unauthenticated ---

func TestFolders_Unauthenticated(t *testing.T) {
	srv := setupTestRouter(t)
	defer srv.Close()

	resp, _ := postJSON(srv.URL+"/api/folders", map[string]any{"name": "Test"}, nil)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	resp, _ = getJSON(srv.URL+"/api/folders", nil)
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}
