package storetest

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/jackc/logger4life/backend/core"
	"github.com/stretchr/testify/require"
)

func RunEntryPages(t *testing.T, ports Ports) {
	ctx := context.Background()
	owner, member, stranger := newUser(t, ports), newUser(t, ports), newUser(t, ports)
	log := newLog(t, ports, owner.ID, "Entry pages")
	other := newLog(t, ports, owner.ID, "Other entries")
	app := core.New(core.Config{Collections: ports})
	ownerCtx := core.WithUserID(ctx, owner.ID)
	start := time.Date(2026, 11, 1, 6, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	ids := []string{}
	for _, stamp := range []time.Time{start.Add(-time.Second), start, start, start.Add(time.Hour), end} {
		entry, err := ports.CreateLogEntry(ctx, newRowID(), owner.ID, log.ID, map[string]any{}, stamp, "**full note**")
		require.NoError(t, err)
		if stamp.Equal(start) {
			ids = append(ids, entry.ID)
		}
	}
	sort.Strings(ids)
	_, err := ports.CreateLogEntry(ctx, newRowID(), owner.ID, other.ID, map[string]any{}, start, "other log")
	require.NoError(t, err)
	for _, order := range []string{"asc", "desc"} {
		p := core.EntryPageParams{LogID: log.ID, StartAt: &start, EndAt: &end, Order: order, CollectionPageParams: core.CollectionPageParams{Limit: 1}}
		seen := []string{}
		for range 4 {
			page, err := core.ListLogEntriesPage.Call(ownerCtx, app, p)
			require.NoError(t, err)
			require.Len(t, page.Entries, 1)
			entry := page.Entries[0]
			require.Equal(t, "**full note**", entry.Note)
			require.Equal(t, owner.Username, entry.Username)
			seen = append(seen, entry.ID)
			p.Cursor = page.NextCursor
			if p.Cursor == "" {
				break
			}
		}
		require.Empty(t, p.Cursor)
		require.Len(t, seen, 3)
		if order == "asc" {
			require.Equal(t, ids, seen[:2])
		} else {
			require.Equal(t, []string{ids[1], ids[0]}, seen[1:])
		}
	}
	page, err := core.ListLogEntriesPage.Call(ownerCtx, app, core.EntryPageParams{LogID: log.ID})
	require.NoError(t, err)
	require.Len(t, page.Entries, 5)
	require.True(t, page.Entries[0].OccurredAt.Equal(end))
	require.Empty(t, page.NextCursor)
	_, err = core.ListLogEntriesPage.Call(core.WithUserID(ctx, stranger.ID), app, core.EntryPageParams{LogID: log.ID})
	require.ErrorIs(t, err, core.ErrLogNotFound)
	token := []byte("entry-pages-" + owner.ID)
	require.NoError(t, ports.CreateShareToken(ctx, owner.ID, log.ID, token))
	shareID := newRowID()
	_, err = ports.JoinSharedLog(ctx, shareID, member.ID, token)
	require.NoError(t, err)
	memberCtx := core.WithUserID(ctx, member.ID)
	p := core.EntryPageParams{LogID: log.ID, CollectionPageParams: core.CollectionPageParams{Limit: 1}}
	page, err = core.ListLogEntriesPage.Call(memberCtx, app, p)
	require.NoError(t, err)
	require.NotEmpty(t, page.NextCursor)
	p.Cursor = page.NextCursor
	require.NoError(t, ports.RemoveSharedUser(ctx, owner.ID, log.ID, shareID))
	_, err = core.ListLogEntriesPage.Call(memberCtx, app, p)
	require.ErrorIs(t, err, core.ErrLogNotFound)
	empty := newLog(t, ports, owner.ID, "Empty entries")
	page, err = core.ListLogEntriesPage.Call(ownerCtx, app, core.EntryPageParams{LogID: empty.ID})
	require.NoError(t, err)
	require.NotNil(t, page.Entries)
	require.Empty(t, page.Entries)
	require.Empty(t, page.NextCursor)
}
