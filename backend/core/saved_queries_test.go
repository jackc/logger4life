package core

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeSavedQueryStore struct {
	scopeUserID string
	id          string
	name        string
	queryText   string
	query       SavedQuery
	queries     []SavedQuery
	err         error
	calls       int
}

func (s *fakeSavedQueryStore) ListSavedQueries(_ context.Context, userID string) ([]SavedQuery, error) {
	s.calls++
	s.scopeUserID = userID
	return s.queries, s.err
}

func (s *fakeSavedQueryStore) GetSavedQueryByName(_ context.Context, userID, name string) (SavedQuery, error) {
	s.calls++
	s.scopeUserID, s.name = userID, name
	return s.query, s.err
}

func (s *fakeSavedQueryStore) CreateSavedQuery(_ context.Context, userID, name, queryText string) (SavedQuery, error) {
	s.calls++
	s.scopeUserID, s.name, s.queryText = userID, name, queryText
	return s.query, s.err
}

func (s *fakeSavedQueryStore) UpdateSavedQuery(_ context.Context, userID, id, name, queryText string) (SavedQuery, error) {
	s.calls++
	s.scopeUserID, s.id, s.name, s.queryText = userID, id, name, queryText
	return s.query, s.err
}

func (s *fakeSavedQueryStore) DeleteSavedQuery(_ context.Context, userID, id string) error {
	s.calls++
	s.scopeUserID, s.id = userID, id
	return s.err
}

func TestSavedQueryActionsScopeStoreCallsToContextUser(t *testing.T) {
	store := &fakeSavedQueryStore{
		query:   SavedQuery{ID: testID("query-1"), Name: "Daily doses"},
		queries: []SavedQuery{{ID: testID("query-1")}},
	}
	app := New(Config{SavedQueries: store})
	ctx := WithUserID(context.Background(), "user-1")

	queries, err := ListSavedQueries.Call(ctx, app, ListSavedQueriesParams{})
	if err != nil || len(queries) != 1 || store.scopeUserID != "user-1" {
		t.Fatalf("ListSavedQueries() = %#v, %v (scope %q)", queries, err, store.scopeUserID)
	}

	got, err := GetSavedQuery.Call(ctx, app, GetSavedQueryParams{Name: "Daily doses"})
	if err != nil || got.ID != testID("query-1") || store.name != "Daily doses" {
		t.Fatalf("GetSavedQuery() = %#v, %v (name %q)", got, err, store.name)
	}

	created := SavedQueryParams{Name: "Weekly", QueryText: "SELECT 1"}
	if _, err := CreateSavedQuery.Call(ctx, app, created); err != nil {
		t.Fatal(err)
	}
	if store.scopeUserID != "user-1" || store.name != "Weekly" || store.queryText != "SELECT 1" {
		t.Fatalf("CreateSavedQuery store call = (%q, %q, %q)", store.scopeUserID, store.name, store.queryText)
	}

	update := UpdateSavedQueryParams{ID: testID("query-1"), Name: "Monthly", QueryText: "SELECT 2"}
	if _, err := UpdateSavedQuery.Call(ctx, app, update); err != nil {
		t.Fatal(err)
	}
	if store.id != testID("query-1") || store.name != "Monthly" || store.queryText != "SELECT 2" {
		t.Fatalf("UpdateSavedQuery store call = (%q, %q, %q)", store.id, store.name, store.queryText)
	}

	if _, err := DeleteSavedQuery.Call(ctx, app, DeleteSavedQueryParams{ID: testID("query-2")}); err != nil {
		t.Fatal(err)
	}
	if store.scopeUserID != "user-1" || store.id != testID("query-2") {
		t.Fatalf("DeleteSavedQuery store call = (%q, %q)", store.scopeUserID, store.id)
	}
}

// Create and update share one rule set, so a saved query cannot be edited into
// a shape it could not have been created in.
func TestSavedQueryValidationRunsBeforeStore(t *testing.T) {
	store := &fakeSavedQueryStore{}
	app := New(Config{SavedQueries: store})
	ctx := WithUserID(context.Background(), "user-1")

	rejected := []struct {
		name   string
		params SavedQueryParams
	}{
		{"blank name", SavedQueryParams{Name: "  ", QueryText: "SELECT 1"}},
		{"name too long", SavedQueryParams{Name: strings.Repeat("x", 101), QueryText: "SELECT 1"}},
		{"blank query", SavedQueryParams{Name: "Weekly", QueryText: "   "}},
		{"query too long", SavedQueryParams{Name: "Weekly", QueryText: strings.Repeat("x", 10001)}},
	}
	for _, c := range rejected {
		var validationErr *ValidationError
		if _, err := CreateSavedQuery.Call(ctx, app, c.params); !errors.As(err, &validationErr) {
			t.Fatalf("create_saved_query %s: error = %T %v, want ValidationError", c.name, err, err)
		}
		update := UpdateSavedQueryParams{ID: testID("query-1"), Name: c.params.Name, QueryText: c.params.QueryText}
		if _, err := UpdateSavedQuery.Call(ctx, app, update); !errors.As(err, &validationErr) {
			t.Fatalf("update_saved_query %s: error = %T %v, want ValidationError", c.name, err, err)
		}
	}
	if store.calls != 0 {
		t.Fatalf("invalid params reached the store %d times", store.calls)
	}

	if _, err := CreateSavedQuery.Call(ctx, app, SavedQueryParams{Name: "  Weekly  ", QueryText: "SELECT 1"}); err != nil {
		t.Fatal(err)
	}
	if store.name != "Weekly" {
		t.Fatalf("stored name = %q, want the trimmed name", store.name)
	}
}

func TestSavedQueryActionsRequireAuthenticationBeforeCallingStore(t *testing.T) {
	store := &fakeSavedQueryStore{}
	app := New(Config{SavedQueries: store})
	ctx := context.Background()

	calls := []struct {
		name string
		call func() error
	}{
		{"list_saved_queries", func() error { _, e := ListSavedQueries.Call(ctx, app, ListSavedQueriesParams{}); return e }},
		{"get_saved_query", func() error {
			_, e := GetSavedQuery.Call(ctx, app, GetSavedQueryParams{Name: "Weekly"})
			return e
		}},
		{"create_saved_query", func() error {
			_, e := CreateSavedQuery.Call(ctx, app, SavedQueryParams{Name: "Weekly", QueryText: "SELECT 1"})
			return e
		}},
		{"update_saved_query", func() error {
			_, e := UpdateSavedQuery.Call(ctx, app, UpdateSavedQueryParams{ID: testID("q-1"), Name: "Weekly", QueryText: "SELECT 1"})
			return e
		}},
		{"delete_saved_query", func() error {
			_, e := DeleteSavedQuery.Call(ctx, app, DeleteSavedQueryParams{ID: testID("q-1")})
			return e
		}},
	}
	for _, c := range calls {
		if err := c.call(); !errors.Is(err, ErrUnauthenticated) {
			t.Fatalf("%s error = %v, want ErrUnauthenticated", c.name, err)
		}
	}
	if store.calls != 0 {
		t.Fatalf("unauthenticated calls reached the store %d times", store.calls)
	}
}

func TestSavedQueryActionsPropagateStoreSentinels(t *testing.T) {
	store := &fakeSavedQueryStore{err: ErrSavedQueryNameTaken}
	app := New(Config{SavedQueries: store})
	ctx := WithUserID(context.Background(), "user-1")

	if _, err := CreateSavedQuery.Call(ctx, app, SavedQueryParams{Name: "Weekly", QueryText: "SELECT 1"}); !errors.Is(err, ErrSavedQueryNameTaken) {
		t.Fatalf("create_saved_query error = %v, want ErrSavedQueryNameTaken", err)
	}
	store.err = ErrSavedQueryNotFound
	if _, err := GetSavedQuery.Call(ctx, app, GetSavedQueryParams{Name: "Missing"}); !errors.Is(err, ErrSavedQueryNotFound) {
		t.Fatalf("get_saved_query error = %v, want ErrSavedQueryNotFound", err)
	}
	if _, err := DeleteSavedQuery.Call(ctx, app, DeleteSavedQueryParams{ID: testID("q-1")}); !errors.Is(err, ErrSavedQueryNotFound) {
		t.Fatalf("delete_saved_query error = %v, want ErrSavedQueryNotFound", err)
	}
}
