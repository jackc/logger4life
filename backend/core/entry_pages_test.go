package core

import (
	"context"
	"encoding/base64"
	"encoding/json/v2"
	"strings"
	"testing"
	"time"

	"github.com/jackc/logger4life/backend/domain"
	"github.com/stretchr/testify/require"
)

type entryPageFixture struct {
	CollectionStore
	rows  []domain.LogEntry
	calls int
	query EntryPageQuery
}

func (f *entryPageFixture) ListLogEntriesPage(_ context.Context, _ string, p EntryPageQuery) ([]domain.LogEntry, error) {
	f.calls++
	f.query = p
	start := 0
	for i, e := range f.rows {
		if e.ID == p.AfterID {
			start = i + 1
		}
	}
	return f.rows[start:min(len(f.rows), start+p.Limit)], nil
}
func TestEntryPageCursorScopeAndBounds(t *testing.T) {
	stamp := time.Date(2026, 11, 1, 6, 0, 0, 0, time.UTC)
	f := &entryPageFixture{rows: []domain.LogEntry{{ID: testID("one"), OccurredAt: stamp}, {ID: testID("two"), OccurredAt: stamp}}}
	app := New(Config{Collections: f})
	ctx := WithUserID(context.Background(), "alice")
	p := EntryPageParams{LogID: testID("log"), StartAt: &stamp, CollectionPageParams: CollectionPageParams{Limit: 1}}
	page, err := ListLogEntriesPage.Call(ctx, app, p)
	require.NoError(t, err)
	require.NotEmpty(t, page.NextCursor)
	require.Equal(t, 2, f.query.Limit)
	require.Equal(t, "desc", f.query.Order)
	p.Cursor = page.NextCursor
	cases := []EntryPageParams{p, p, p, p, p}
	cases[0].LogID = testID("other")
	cases[1].Order = "asc"
	cases[2].StartAt = nil
	end := stamp.Add(time.Hour)
	cases[3].EndAt = &end
	cases[4].Cursor = "invalid"
	calls := f.calls
	for _, bad := range cases {
		_, err = ListLogEntriesPage.Call(ctx, app, bad)
		require.ErrorContains(t, err, "invalid cursor")
	}
	_, err = ListLogEntriesPage.Call(WithUserID(context.Background(), "bob"), app, p)
	require.ErrorContains(t, err, "invalid cursor")
	require.Equal(t, calls, f.calls)
	raw, err := base64.RawURLEncoding.DecodeString(p.Cursor)
	require.NoError(t, err)
	var cursor collectionCursor
	require.NoError(t, json.Unmarshal(raw, &cursor))
	for _, bad := range []collectionCursor{
		{Version: cursor.Version, Collection: cursor.Collection, UserID: cursor.UserID, Name: "bad-time", ID: cursor.ID},
		{Version: cursor.Version, Collection: cursor.Collection, UserID: cursor.UserID, Name: cursor.Name, ID: "bad-id"},
	} {
		raw, err := json.Marshal(bad)
		require.NoError(t, err)
		badParams := p
		badParams.Cursor = base64.RawURLEncoding.EncodeToString(raw)
		_, err = ListLogEntriesPage.Call(ctx, app, badParams)
		require.ErrorContains(t, err, "invalid cursor")
	}
	// Equivalent offsets and an explicit default order keep the same window.
	offset := stamp.In(time.FixedZone("offset", -5*60*60))
	p.StartAt = &offset
	p.Order = "desc"
	p.Limit = 100
	page, err = ListLogEntriesPage.Call(ctx, app, p)
	require.NoError(t, err)
	require.Len(t, page.Entries, 1)
	require.Empty(t, page.NextCursor)
	require.True(t, f.query.AfterAt.Equal(stamp))
}
func TestEntryPageValidation(t *testing.T) {
	stamp := time.Now()
	zero := time.Time{}
	for _, p := range []EntryPageParams{
		{LogID: "bad"},
		{LogID: testID("log"), Order: "bad"},
		{LogID: testID("log"), StartAt: &zero},
		{LogID: testID("log"), StartAt: &stamp, EndAt: &stamp},
		{LogID: testID("log"), CollectionPageParams: CollectionPageParams{Limit: -1}},
		{LogID: testID("log"), CollectionPageParams: CollectionPageParams{Limit: 101}},
		{LogID: testID("log"), CollectionPageParams: CollectionPageParams{Cursor: strings.Repeat("x", 1025)}},
	} {
		require.Error(t, p.Validate())
	}
	p := EntryPageParams{LogID: testID("log")}
	require.NoError(t, p.Validate())
	require.Equal(t, 50, p.Limit)
	require.Equal(t, "desc", p.Order)
	_, err := ListLogEntriesPage.Call(context.Background(), New(Config{}), p)
	require.Error(t, err)
}
func TestEntryPageByteLimitDoesNotSkipEntries(t *testing.T) {
	f := &entryPageFixture{}
	for i := range 12 {
		f.rows = append(f.rows, domain.LogEntry{ID: testID(string(rune('a' + i))), OccurredAt: time.Now().UTC(), Note: strings.Repeat("<\u2028", 10000)})
	}
	app := New(Config{Collections: f})
	ctx := WithUserID(context.Background(), "alice")
	p := EntryPageParams{LogID: testID("log"), CollectionPageParams: CollectionPageParams{Limit: 100}}
	seen := []string{}
	for range 12 {
		page, err := ListLogEntriesPage.Call(ctx, app, p)
		require.NoError(t, err)
		require.Less(t, len(page.Entries), 12)
		for _, e := range page.Entries {
			seen = append(seen, e.ID)
		}
		p.Cursor = page.NextCursor
		if p.Cursor == "" {
			break
		}
	}
	require.Empty(t, p.Cursor)
	expected := []string{}
	for _, e := range f.rows {
		expected = append(expected, e.ID)
	}
	require.Equal(t, expected, seen)
	f.rows[0].Note = strings.Repeat("x", MaxCollectionPageBytes)
	_, err := ListLogEntriesPage.Call(ctx, app, EntryPageParams{LogID: p.LogID})
	require.ErrorContains(t, err, "record exceeds")
}
