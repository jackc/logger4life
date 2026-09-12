package core

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type collectionFixture struct {
	CollectionStore
	rows  []SavedQuerySummary
	calls int
	limit int
}

func (f *collectionFixture) ListSavedQueriesPage(_ context.Context, _, _, id string, limit int) ([]SavedQuerySummary, error) {
	f.calls++
	f.limit = limit
	var result []SavedQuerySummary
	for _, row := range f.rows {
		if row.ID > id {
			result = append(result, row)
		}
		if len(result) == limit {
			break
		}
	}
	return result, nil
}

func TestCollectionPagesByteLimitAndCursor(t *testing.T) {
	f := &collectionFixture{}
	for i := range 40 {
		f.rows = append(f.rows, SavedQuerySummary{ID: fmt.Sprintf("%03d", i), Name: "same", SortName: "same", QueryText: strings.Repeat(`"<&`, 3000)})
	}
	app := New(Config{Collections: f})
	ctx := WithUserID(context.Background(), "alice")
	cursor := ""
	seen := map[string]bool{}
	for range 40 {
		page, err := ListSavedQueriesPage.Call(ctx, app, CollectionPageParams{Limit: 100, Cursor: cursor})
		require.NoError(t, err)
		encoded, err := json.Marshal(page)
		require.NoError(t, err)
		require.LessOrEqual(t, len(encoded), MaxCollectionPageBytes)
		require.Equal(t, 101, f.limit)
		for _, q := range page.Queries {
			require.False(t, seen[q.ID])
			seen[q.ID] = true
		}
		cursor = page.NextCursor
		if cursor == "" {
			break
		}
		_, err = ListSavedQueriesPage.Call(WithUserID(context.Background(), "bob"), app, CollectionPageParams{Cursor: cursor})
		require.ErrorContains(t, err, "invalid cursor")
		_, err = ListLogsPage.Call(ctx, app, CollectionPageParams{Cursor: cursor})
		require.ErrorContains(t, err, "invalid cursor")
	}
	require.Empty(t, cursor)
	require.Len(t, seen, 40)
	f.calls = 0
	for _, p := range []CollectionPageParams{{Limit: -1}, {Limit: 101}, {Cursor: "invalid"}, {Cursor: strings.Repeat("x", 1025)}} {
		_, err := ListSavedQueriesPage.Call(ctx, app, p)
		require.Error(t, err)
	}
	require.Zero(t, f.calls)
	f.rows = []SavedQuerySummary{{ID: "big", QueryText: strings.Repeat("x", MaxCollectionPageBytes)}}
	_, err := ListSavedQueriesPage.Call(ctx, app, CollectionPageParams{})
	require.ErrorContains(t, err, "record exceeds")
}
