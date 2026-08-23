package core

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/logger4life/backend/domain"
)

type fakeLogStore struct {
	scopeUserID string
	logID       string
	name        string
	fields      []domain.FieldDefinition
	log         Log
	logs        []Log
	err         error
	calls       int
}

func (s *fakeLogStore) CreateLog(_ context.Context, _ string, userID, name string, fields []domain.FieldDefinition) (Log, error) {
	s.calls++
	s.scopeUserID, s.name, s.fields = userID, name, fields
	return s.log, s.err
}

func (s *fakeLogStore) GetLog(_ context.Context, userID, logID string) (Log, error) {
	s.calls++
	s.scopeUserID, s.logID = userID, logID
	return s.log, s.err
}

func (s *fakeLogStore) UpdateLog(_ context.Context, userID, logID, name string, fields []domain.FieldDefinition) (Log, error) {
	s.calls++
	s.scopeUserID, s.logID, s.name, s.fields = userID, logID, name, fields
	return s.log, s.err
}

func (s *fakeLogStore) DeleteLog(_ context.Context, userID, logID string) error {
	s.calls++
	s.scopeUserID, s.logID = userID, logID
	return s.err
}

func (s *fakeLogStore) ListLogs(_ context.Context, userID string) ([]Log, error) {
	s.calls++
	s.scopeUserID = userID
	return s.logs, s.err
}

// Every log action must scope the store call to the caller in context. No log
// action takes a user ID parameter, so a caller cannot ask to act as someone
// else; this test fails if one ever starts forwarding an attacker-supplied ID.
func TestLogActionsScopeStoreCallsToContextUser(t *testing.T) {
	store := &fakeLogStore{log: Log{ID: testID("log-1")}, logs: []Log{{ID: testID("log-1")}}}
	app := New(Config{Logs: store})
	ctx := WithUserID(context.Background(), "user-1")

	if _, err := CreateLog.Call(ctx, app, CreateLogParams{Name: "Vitamins"}); err != nil {
		t.Fatal(err)
	}
	if store.scopeUserID != "user-1" || store.name != "Vitamins" {
		t.Fatalf("CreateLog store call = (%q, %q)", store.scopeUserID, store.name)
	}

	if _, err := GetLog.Call(ctx, app, GetLogParams{LogID: testID("log-1")}); err != nil {
		t.Fatal(err)
	}
	if store.scopeUserID != "user-1" || store.logID != testID("log-1") {
		t.Fatalf("GetLog store call = (%q, %q)", store.scopeUserID, store.logID)
	}

	if _, err := UpdateLog.Call(ctx, app, UpdateLogParams{LogID: testID("log-2"), Name: "Pushups"}); err != nil {
		t.Fatal(err)
	}
	if store.scopeUserID != "user-1" || store.logID != testID("log-2") || store.name != "Pushups" {
		t.Fatalf("UpdateLog store call = (%q, %q, %q)", store.scopeUserID, store.logID, store.name)
	}

	if _, err := DeleteLog.Call(ctx, app, DeleteLogParams{LogID: testID("log-3")}); err != nil {
		t.Fatal(err)
	}
	if store.scopeUserID != "user-1" || store.logID != testID("log-3") {
		t.Fatalf("DeleteLog store call = (%q, %q)", store.scopeUserID, store.logID)
	}

	logs, err := ListLogs.Call(ctx, app, ListLogsParams{})
	if err != nil || len(logs) != 1 || logs[0].ID != testID("log-1") {
		t.Fatalf("ListLogs() = %#v, %v", logs, err)
	}
	if store.scopeUserID != "user-1" {
		t.Fatalf("ListLogs scope = %q", store.scopeUserID)
	}
}

func TestLogActionsRequireAuthenticationBeforeCallingStore(t *testing.T) {
	store := &fakeLogStore{}
	app := New(Config{Logs: store})
	ctx := context.Background()

	calls := []struct {
		name string
		call func() error
	}{
		{"create_log", func() error { _, e := CreateLog.Call(ctx, app, CreateLogParams{Name: "Vitamins"}); return e }},
		{"get_log", func() error { _, e := GetLog.Call(ctx, app, GetLogParams{LogID: testID("log-1")}); return e }},
		{"update_log", func() error {
			_, e := UpdateLog.Call(ctx, app, UpdateLogParams{LogID: testID("log-1"), Name: "V"})
			return e
		}},
		{"delete_log", func() error { _, e := DeleteLog.Call(ctx, app, DeleteLogParams{LogID: testID("log-1")}); return e }},
		{"list_logs", func() error { _, e := ListLogs.Call(ctx, app, ListLogsParams{}); return e }},
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

// Name and field rules are enforced before persistence, and update reuses the
// create rules so the two cannot drift apart.
func TestLogParamsValidationRunsBeforeStore(t *testing.T) {
	store := &fakeLogStore{}
	app := New(Config{Logs: store})
	ctx := WithUserID(context.Background(), "user-1")

	rejected := []struct {
		name   string
		params CreateLogParams
	}{
		{"blank name", CreateLogParams{Name: "   "}},
		{"name too long", CreateLogParams{Name: strings.Repeat("x", 101)}},
		{"unknown field type", CreateLogParams{Name: "Vitamins", Fields: []domain.FieldDefinition{{Name: "dose", Type: "date"}}}},
		{"duplicate field names", CreateLogParams{Name: "Vitamins", Fields: []domain.FieldDefinition{
			{Name: "dose", Type: "text"}, {Name: "DOSE", Type: "text"},
		}}},
	}
	for _, c := range rejected {
		var validationErr *ValidationError
		if _, err := CreateLog.Call(ctx, app, c.params); !errors.As(err, &validationErr) {
			t.Fatalf("create_log %s: error = %T %v, want ValidationError", c.name, err, err)
		}
		update := UpdateLogParams{LogID: testID("log-1"), Name: c.params.Name, Fields: c.params.Fields}
		if _, err := UpdateLog.Call(ctx, app, update); !errors.As(err, &validationErr) {
			t.Fatalf("update_log %s: error = %T %v, want ValidationError", c.name, err, err)
		}
	}
	if store.calls != 0 {
		t.Fatalf("invalid params reached the store %d times", store.calls)
	}
}

// A log with no custom fields must reach the store as an empty slice, not nil:
// the store serializes this straight to JSONB, where nil would become null.
func TestCreateLogNormalizesNameAndFields(t *testing.T) {
	store := &fakeLogStore{}
	app := New(Config{Logs: store})
	ctx := WithUserID(context.Background(), "user-1")

	if _, err := CreateLog.Call(ctx, app, CreateLogParams{Name: "  Vitamins  "}); err != nil {
		t.Fatal(err)
	}
	if store.name != "Vitamins" {
		t.Fatalf("stored name = %q, want the trimmed name", store.name)
	}
	if store.fields == nil || len(store.fields) != 0 {
		t.Fatalf("stored fields = %#v, want an empty slice", store.fields)
	}
}

func TestLogActionsPropagateStoreSentinels(t *testing.T) {
	store := &fakeLogStore{}
	app := New(Config{Logs: store})
	ctx := WithUserID(context.Background(), "user-1")

	store.err = ErrLogNameTaken
	if _, err := CreateLog.Call(ctx, app, CreateLogParams{Name: "Vitamins"}); !errors.Is(err, ErrLogNameTaken) {
		t.Fatalf("create_log error = %v, want ErrLogNameTaken", err)
	}
	store.err = ErrLogNotFound
	if _, err := GetLog.Call(ctx, app, GetLogParams{LogID: testID("log-1")}); !errors.Is(err, ErrLogNotFound) {
		t.Fatalf("get_log error = %v, want ErrLogNotFound", err)
	}
	if _, err := DeleteLog.Call(ctx, app, DeleteLogParams{LogID: testID("log-1")}); !errors.Is(err, ErrLogNotFound) {
		t.Fatalf("delete_log error = %v, want ErrLogNotFound", err)
	}
}
