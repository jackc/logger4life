package server

import (
	"context"
	"encoding/json/v2"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/logger4life/backend/core"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

func mcpActionClient(t *testing.T, app *core.Core, user core.User) *mcp.ClientSession {
	t.Helper()
	ct, st := mcp.NewInMemoryTransports()
	ctx := context.WithValue(context.Background(), userContextKey, &AuthUser{ID: user.ID, Username: user.Username})
	ss, err := newMCPRegistry(app).Server().Connect(ctx, st, nil)
	require.NoError(t, err)
	t.Cleanup(func() { ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "write-test", Version: "1"}, nil).Connect(context.Background(), ct, nil)
	require.NoError(t, err)
	t.Cleanup(func() { cs.Close() })
	return cs
}
func mcpActionCall(t *testing.T, c *mcp.ClientSession, name string, args map[string]any) map[string]any {
	t.Helper()
	r, err := c.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	require.NoError(t, err)
	require.False(t, r.IsError, "%s: %v", name, r.Content)
	return r.StructuredContent.(map[string]any)
}
func mcpActionError(t *testing.T, c *mcp.ClientSession, name string, args map[string]any, want string) {
	t.Helper()
	r, err := c.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	require.NoError(t, err)
	require.True(t, r.IsError, "%s", name)
	require.Contains(t, r.Content[0].(*mcp.TextContent).Text, want)
}
func mcpTestUser(t *testing.T, app *core.Core, name string) core.User {
	t.Helper()
	u, err := core.RegisterUser.Call(context.Background(), app, core.RegisterUserParams{Username: name, Password: "password123"})
	require.NoError(t, err)
	return u.User
}
func TestMCPWriteLifecycle(t *testing.T) {
	s := setupTestRouter(t)
	defer s.Close()
	owner := mcpTestUser(t, s.app, "mcp_owner")
	member := mcpTestUser(t, s.app, "mcp_member")
	c := mcpActionClient(t, s.app, owner)
	other := mcpActionClient(t, s.app, member)
	defs := []any{map[string]any{"name": "count", "type": "number", "required": true}, map[string]any{"name": "context", "type": "text", "required": false}}
	create := map[string]any{"name": "Workout", "fields": defs, "idempotency_key": "log-1"}
	log := mcpActionCall(t, c, "create_log", create)
	id := log["id"].(string)
	require.Equal(t, log, mcpActionCall(t, c, "create_log", create))
	require.Equal(t, log, mcpActionCall(t, c, "get_log", map[string]any{"log_id": id}))
	mcpActionError(t, other, "get_log", map[string]any{"log_id": id}, "log not found")
	mcpActionError(t, c, "create_log", map[string]any{"name": "Different", "fields": defs, "idempotency_key": "log-1"}, "different arguments")
	otherLog := mcpActionCall(t, other, "create_log", create)
	require.NotEqual(t, id, otherLog["id"])
	mcpActionError(t, c, "create_log_entry", map[string]any{"log_id": id, "idempotency_key": strings.Repeat("x", 129)}, "at most 128")
	mcpActionError(t, c, "create_log_entry", map[string]any{"log_id": id, "occurred_at": "yesterday"}, "time")
	stamp := "2026-10-01T10:30:00-05:00"
	entryArgs := map[string]any{"log_id": id, "fields": map[string]any{"count": "25", "context": "gym"}, "note": "**Workout**", "occurred_at": stamp, "idempotency_key": "log-1"}
	entry := mcpActionCall(t, c, "create_log_entry", entryArgs)
	eid := entry["id"].(string)
	assertMCPTime(t, "2026-10-01T15:30:00Z", entry["occurred_at"])
	require.Equal(t, entry, mcpActionCall(t, c, "create_log_entry", entryArgs))
	patch := mcpActionCall(t, c, "update_log_entry", map[string]any{"log_id": id, "entry_id": eid, "fields": map[string]any{"count": "30"}})
	require.Equal(t, map[string]any{"count": "30", "context": "gym"}, patch["fields"])
	require.Equal(t, entry["occurred_at"], patch["occurred_at"])
	require.Equal(t, entry["note"], patch["note"])
	patch = mcpActionCall(t, c, "update_log_entry", map[string]any{"log_id": id, "entry_id": eid, "fields": map[string]any{"context": nil}, "note": ""})
	require.Equal(t, map[string]any{"count": "30"}, patch["fields"])
	require.Equal(t, "", patch["note"])
	mcpActionError(t, c, "update_log_entry", map[string]any{"log_id": id, "entry_id": eid, "fields": map[string]any{"count": nil}}, "required")
	mcpActionError(t, c, "update_log_entry", map[string]any{"log_id": id, "entry_id": eid, "fields": map[string]any{"bogus": nil}}, "unknown field")
	require.Equal(t, patch, mcpActionCall(t, c, "get_log_entry", map[string]any{"log_id": id, "entry_id": eid}))
	patch = mcpActionCall(t, c, "update_log_entry", map[string]any{"log_id": id, "entry_id": eid, "occurred_at": "2026-10-02T00:00:00Z"})
	assertMCPTime(t, "2026-10-02T00:00:00Z", patch["occurred_at"])
	// Failed validation rolls back the receipt, allowing corrected arguments.
	mcpActionError(t, c, "create_log_entry", map[string]any{"log_id": id, "idempotency_key": "corrected"}, "required")
	now := mcpActionCall(t, c, "create_log_entry", map[string]any{"log_id": id, "fields": map[string]any{"count": "1"}, "idempotency_key": "corrected"})
	occurred, err := time.Parse(time.RFC3339Nano, now["occurred_at"].(string))
	require.NoError(t, err)
	require.WithinDuration(t, time.Now(), occurred, 10*time.Second)
	// Organization writes return the committed placement or folder.
	folder := mcpActionCall(t, c, "create_folder", map[string]any{"name": "Health", "parent_folder_id": nil, "idempotency_key": "folder-1"})
	fid := folder["id"].(string)
	require.Equal(t, folder, mcpActionCall(t, c, "create_folder", map[string]any{"name": "Health", "parent_folder_id": nil, "idempotency_key": "folder-1"}))
	require.Equal(t, "Fitness", mcpActionCall(t, c, "rename_folder", map[string]any{"folder_id": fid, "name": "Fitness"})["name"])
	child := mcpActionCall(t, c, "create_folder", map[string]any{"name": "Child", "parent_folder_id": nil})
	moved := mcpActionCall(t, c, "move_folder", map[string]any{"folder_id": child["id"], "parent_folder_id": fid, "position": 0})
	require.Equal(t, fid, moved["parent_folder_id"])
	require.Equal(t, moved, mcpActionCall(t, c, "get_folder", map[string]any{"folder_id": child["id"]}))
	require.Len(t, mcpActionCall(t, c, "list_folders", map[string]any{})["folders"], 2)
	require.Equal(t, fid, mcpActionCall(t, c, "update_log_placement", map[string]any{"log_id": id, "folder_id": fid, "position": 0})["folder_id"])
	require.Equal(t, false, mcpActionCall(t, c, "pin_log", map[string]any{"log_id": id, "pinned": false})["pinned_to_home"])
	mcpActionError(t, c, "update_log_home_position", map[string]any{"log_id": id, "home_position": 0}, "not pinned")
	require.Equal(t, true, mcpActionCall(t, c, "pin_log", map[string]any{"log_id": id, "pinned": true})["pinned_to_home"])
	require.Equal(t, float64(0), mcpActionCall(t, c, "update_log_home_position", map[string]any{"log_id": id, "home_position": 0})["home_position"])
	require.Equal(t, "Training", mcpActionCall(t, c, "update_log", map[string]any{"log_id": id, "name": "Training", "fields": defs})["name"])
	mcpActionError(t, c, "delete_folder", map[string]any{"folder_id": fid}, "not empty")
	// Sharing reads and writes preserve ownership restrictions.
	shared := mcpActionCall(t, c, "create_share_token", map[string]any{"log_id": id, "idempotency_key": "invite-1"})
	token := shared["share_token"].(string)
	require.Equal(t, shared, mcpActionCall(t, c, "create_share_token", map[string]any{"log_id": id, "idempotency_key": "invite-1"}))
	require.Equal(t, id, mcpActionCall(t, other, "get_share_info", map[string]any{"token": token})["log_id"])
	joined := mcpActionCall(t, other, "join_shared_log", map[string]any{"token": token})
	require.Equal(t, id, joined["id"])
	require.Equal(t, false, joined["is_owner"])
	require.Nil(t, joined["share_token"])
	mcpActionError(t, other, "delete_log", map[string]any{"log_id": id}, "log not found")
	mcpActionError(t, other, "create_share_token", map[string]any{"log_id": id}, "log not found")
	mcpActionCall(t, other, "update_log_entry", map[string]any{"log_id": id, "entry_id": eid, "note": "shared edit"})
	users := mcpActionCall(t, c, "list_shared_users", map[string]any{"log_id": id})["users"].([]any)
	require.Len(t, users, 1)
	mcpActionCall(t, c, "remove_shared_user", map[string]any{"log_id": id, "share_id": users[0].(map[string]any)["id"]})
	mcpActionError(t, other, "get_log_entry", map[string]any{"log_id": id, "entry_id": eid}, "log not found")
	mcpActionError(t, other, "update_log_entry", map[string]any{"log_id": id, "entry_id": eid, "note": "denied"}, "log not found")
	require.Nil(t, mcpActionCall(t, c, "delete_share_token", map[string]any{"log_id": id})["share_token"])
	mcpActionError(t, other, "join_shared_log", map[string]any{"token": token}, "invalid share link")
	deleted := mcpActionCall(t, c, "delete_log_entry", map[string]any{"log_id": id, "entry_id": eid, "idempotency_key": "delete-1"})
	require.Equal(t, true, deleted["deleted"])
	require.Equal(t, deleted, mcpActionCall(t, c, "delete_log_entry", map[string]any{"log_id": id, "entry_id": eid, "idempotency_key": "delete-1"}))
	mcpActionError(t, c, "get_log_entry", map[string]any{"log_id": id, "entry_id": eid}, "entry not found")
	mcpActionCall(t, c, "delete_log", map[string]any{"log_id": id})
	mcpActionCall(t, c, "delete_folder", map[string]any{"folder_id": child["id"]})
	mcpActionCall(t, c, "delete_folder", map[string]any{"folder_id": fid})
}

func TestMCPWriteConcurrentRetriesAndPatches(t *testing.T) {
	s := setupTestRouter(t)
	defer s.Close()
	owner := mcpTestUser(t, s.app, "concurrent_owner")
	c := mcpActionClient(t, s.app, owner)
	log := mcpActionCall(t, c, "create_log", map[string]any{"name": "Concurrent", "fields": []any{map[string]any{"name": "a", "type": "text", "required": false}, map[string]any{"name": "b", "type": "text", "required": false}}})
	args := map[string]any{"log_id": log["id"], "idempotency_key": "same"}
	var wg sync.WaitGroup
	results := make([]*mcp.CallToolResult, 8)
	errs := make([]error, 8)
	for i := range results {
		wg.Go(func() {
			results[i], errs[i] = c.CallTool(context.Background(), &mcp.CallToolParams{Name: "create_log_entry", Arguments: args})
		})
	}
	wg.Wait()
	for i, r := range results {
		require.NoError(t, errs[i])
		require.False(t, r.IsError, "%v", r.Content)
		require.Equal(t, results[0].StructuredContent, r.StructuredContent)
	}
	entry := results[0].StructuredContent.(map[string]any)
	for i, k := range []string{"a", "b"} {
		wg.Go(func() {
			results[i], errs[i] = c.CallTool(context.Background(), &mcp.CallToolParams{Name: "update_log_entry", Arguments: map[string]any{"log_id": log["id"], "entry_id": entry["id"], "fields": map[string]any{k: k}}})
		})
	}
	wg.Wait()
	for i := 0; i < 2; i++ {
		require.NoError(t, errs[i])
		require.False(t, results[i].IsError, "%v", results[i].Content)
	}
	updated := mcpActionCall(t, c, "get_log_entry", map[string]any{"log_id": log["id"], "entry_id": entry["id"]})
	require.Equal(t, map[string]any{"a": "a", "b": "b"}, updated["fields"])
	entries, err := core.ListLogEntries.Call(core.WithUserID(context.Background(), owner.ID), s.app, core.ListLogEntriesParams{LogID: log["id"].(string)})
	require.NoError(t, err)
	require.Len(t, entries, 1)
	// A fresh MCP server still returns the original response, even after edits.
	fresh := mcpActionClient(t, s.app, owner)
	require.Equal(t, entry, mcpActionCall(t, fresh, "create_log_entry", args))
}

func TestMCPNewToolsRejectAnonymousCalls(t *testing.T) {
	ct, st := mcp.NewInMemoryTransports()
	ss, err := newMCPRegistry(nil).Server().Connect(context.Background(), st, nil)
	require.NoError(t, err)
	defer ss.Close()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "anonymous", Version: "1"}, nil).Connect(context.Background(), ct, nil)
	require.NoError(t, err)
	defer cs.Close()
	for _, entry := range MCPInventory().Tools {
		encoded, err := json.Marshal(entry.Tool.InputSchema)
		require.NoError(t, err)
		var schema map[string]any
		require.NoError(t, json.Unmarshal(encoded, &schema))
		args := map[string]any{}
		props, _ := schema["properties"].(map[string]any)
		required, _ := schema["required"].([]any)
		for _, name := range required {
			prop := props[name.(string)].(map[string]any)
			typ := prop["type"]
			if _, ok := typ.([]any); ok {
				args[name.(string)] = nil
				continue
			}
			switch typ {
			case "string":
				args[name.(string)] = ""
			case "boolean":
				args[name.(string)] = false
			case "integer":
				args[name.(string)] = 0
			case "object":
				args[name.(string)] = map[string]any{}
			case "array":
				args[name.(string)] = []any{}
			}
		}
		mcpActionError(t, cs, entry.Tool.Name, args, "no authenticated user")
	}
}

func assertMCPTime(t *testing.T, want string, got any) {
	t.Helper()
	expected, err := time.Parse(time.RFC3339Nano, want)
	require.NoError(t, err)
	actual, err := time.Parse(time.RFC3339Nano, got.(string))
	require.NoError(t, err)
	require.True(t, expected.Equal(actual), "expected %s, got %s", want, got)
}
