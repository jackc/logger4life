package storetest

import (
	"context"
	"testing"

	"github.com/jackc/logger4life/backend/core"
	"github.com/stretchr/testify/require"
)

func RunCollectionPages(t *testing.T, ports Ports) {
	ctx := context.Background()
	viewer, owner := newUser(t, ports), newUser(t, ports)
	app := core.New(core.Config{Collections: ports})
	userCtx := core.WithUserID(ctx, viewer.ID)
	first := newLog(t, ports, viewer.ID, "alpha")
	second := newLog(t, ports, viewer.ID, "Beta")
	shared := newLog(t, ports, owner.ID, "Alpha")
	newLog(t, ports, owner.ID, "Private")
	token := []byte("collection-" + owner.ID)
	require.NoError(t, ports.CreateShareToken(ctx, owner.ID, shared.ID, token))
	shareID := newRowID()
	_, err := ports.JoinSharedLog(ctx, shareID, viewer.ID, token)
	require.NoError(t, err)
	seen := []string{}
	cursor := ""
	for range 4 {
		page, err := core.ListLogsPage.Call(userCtx, app, core.CollectionPageParams{Limit: 1, Cursor: cursor})
		require.NoError(t, err)
		require.Len(t, page.Logs, 1)
		seen = append(seen, page.Logs[0].ID)
		cursor = page.NextCursor
		if cursor == "" {
			break
		}
	}
	require.Empty(t, cursor)
	require.ElementsMatch(t, []string{first.ID, second.ID, shared.ID}, seen)
	require.Equal(t, second.ID, seen[2])
	require.Less(t, seen[0], seen[1], "equal folded names must use ID order")
	require.NoError(t, ports.RemoveSharedUser(ctx, owner.ID, shared.ID, shareID))
	page, err := core.ListLogsPage.Call(userCtx, app, core.CollectionPageParams{})
	require.NoError(t, err)
	require.Len(t, page.Logs, 2, "stale placement must not confer access")
	for _, name := range []string{"alpha", "Alpha", "Zulu"} {
		_, err := ports.CreateSavedQuery(ctx, newRowID(), viewer.ID, name, "SELECT 1")
		require.NoError(t, err)
	}
	_, err = ports.CreateSavedQuery(ctx, newRowID(), owner.ID, "Private", "SELECT 2")
	require.NoError(t, err)
	seen = nil
	cursor = ""
	for range 4 {
		page, err := core.ListSavedQueriesPage.Call(userCtx, app, core.CollectionPageParams{Limit: 1, Cursor: cursor})
		require.NoError(t, err)
		require.Len(t, page.Queries, 1)
		seen = append(seen, page.Queries[0].ID)
		cursor = page.NextCursor
		if cursor == "" {
			require.Equal(t, "Zulu", page.Queries[0].Name)
			break
		}
	}
	require.Empty(t, cursor)
	require.Len(t, seen, 3)
	require.Less(t, seen[0], seen[1])
}
