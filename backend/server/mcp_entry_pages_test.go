package server

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMCPListLogEntries(t *testing.T) {
	s := setupTestRouter(t)
	defer s.Close()
	owner := mcpTestUser(t, s.app, "entry_page_owner")
	c := mcpActionClient(t, s.app, owner)
	log := mcpActionCall(t, c, "create_log", map[string]any{"name": "Entries", "fields": []any{map[string]any{"name": "amount", "type": "number", "required": true}}})
	id := log["id"].(string)
	for _, stamp := range []string{"2026-11-01T01:30:00-05:00", "2026-11-01T01:30:00-06:00"} {
		mcpActionCall(t, c, "create_log_entry", map[string]any{"log_id": id, "occurred_at": stamp, "fields": map[string]any{"amount": "2.5"}, "note": "**Meal**"})
	}
	args := map[string]any{"log_id": id, "limit": 1, "start_at": "2026-11-01T00:00:00-05:00", "end_at": "2026-11-02T00:00:00-06:00"}
	page := mcpActionCall(t, c, "list_log_entries", args)
	entries := page["entries"].([]any)
	require.Len(t, entries, 1)
	entry := entries[0].(map[string]any)
	require.Equal(t, mcpActionCall(t, c, "get_log_entry", map[string]any{"log_id": id, "entry_id": entry["id"]}), entry)
	require.Equal(t, "2.5", entry["fields"].(map[string]any)["amount"])
	assertMCPTime(t, "2026-11-01T07:30:00Z", entry["occurred_at"])
	args["cursor"] = page["next_cursor"]
	page = mcpActionCall(t, c, "list_log_entries", args)
	require.Empty(t, page["next_cursor"])
	assertMCPTime(t, "2026-11-01T06:30:00Z", page["entries"].([]any)[0].(map[string]any)["occurred_at"])
	for _, bad := range []map[string]any{
		{"log_id": id, "start_at": "2026-11-01T00:00:00"},
		{"log_id": id, "order": "invalid"},
		{"log_id": id, "limit": 101},
		{"log_id": id, "cursor": "invalid"},
	} {
		mcpActionError(t, c, "list_log_entries", bad, "")
	}
}
