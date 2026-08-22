package core

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/logger4life/backend/domain"
)

type fakeLogEntryStore struct {
	definitionsUserID string
	definitionsLogID  string
	definitions       []domain.FieldDefinition
	definitionsErr    error

	writeUserID  string
	writeLogID   string
	writeEntryID string
	writeFields  map[string]any
	occurredAt   time.Time
	entry        domain.LogEntry
	entries      []domain.LogEntry
	err          error

	definitionCalls int
	writeCalls      int
}

func (s *fakeLogEntryStore) LogFieldDefinitions(_ context.Context, userID, logID string) ([]domain.FieldDefinition, error) {
	s.definitionCalls++
	s.definitionsUserID, s.definitionsLogID = userID, logID
	return s.definitions, s.definitionsErr
}

func (s *fakeLogEntryStore) CreateLogEntry(_ context.Context, userID, logID string, fields map[string]any) (domain.LogEntry, error) {
	s.writeCalls++
	s.writeUserID, s.writeLogID, s.writeFields = userID, logID, fields
	return s.entry, s.err
}

func (s *fakeLogEntryStore) ListLogEntries(_ context.Context, userID, logID string) ([]domain.LogEntry, error) {
	s.writeCalls++
	s.writeUserID, s.writeLogID = userID, logID
	return s.entries, s.err
}

func (s *fakeLogEntryStore) UpdateLogEntry(_ context.Context, userID, logID, entryID string, fields map[string]any, occurredAt time.Time) (domain.LogEntry, error) {
	s.writeCalls++
	s.writeUserID, s.writeLogID, s.writeEntryID = userID, logID, entryID
	s.writeFields, s.occurredAt = fields, occurredAt
	return s.entry, s.err
}

func (s *fakeLogEntryStore) DeleteLogEntry(_ context.Context, userID, logID, entryID string) error {
	s.writeCalls++
	s.writeUserID, s.writeLogID, s.writeEntryID = userID, logID, entryID
	return s.err
}

func doseDefinitions() []domain.FieldDefinition {
	return []domain.FieldDefinition{
		{Name: "dose", Type: "number", Required: true},
		{Name: "notes", Type: "text"},
	}
}

func TestLogEntryActionsScopeStoreCallsToContextUser(t *testing.T) {
	store := &fakeLogEntryStore{
		definitions: doseDefinitions(),
		entry:       domain.LogEntry{ID: "entry-1"},
		entries:     []domain.LogEntry{{ID: "entry-1"}},
	}
	app := New(Config{Entries: store})
	ctx := WithUserID(context.Background(), "user-1")
	occurred := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)

	if _, err := CreateLogEntry.Call(ctx, app, CreateLogEntryParams{LogID: "log-1", Fields: map[string]any{"dose": "500"}}); err != nil {
		t.Fatal(err)
	}
	if store.writeUserID != "user-1" || store.writeLogID != "log-1" {
		t.Fatalf("CreateLogEntry store call = (%q, %q)", store.writeUserID, store.writeLogID)
	}

	entries, err := ListLogEntries.Call(ctx, app, ListLogEntriesParams{LogID: "log-1"})
	if err != nil || len(entries) != 1 || entries[0].ID != "entry-1" {
		t.Fatalf("ListLogEntries() = %#v, %v", entries, err)
	}

	update := UpdateLogEntryParams{LogID: "log-1", EntryID: "entry-1", Fields: map[string]any{"dose": "250"}, OccurredAt: occurred}
	if _, err := UpdateLogEntry.Call(ctx, app, update); err != nil {
		t.Fatal(err)
	}
	if store.writeEntryID != "entry-1" || !store.occurredAt.Equal(occurred) {
		t.Fatalf("UpdateLogEntry store call = (%q, %v)", store.writeEntryID, store.occurredAt)
	}

	if _, err := DeleteLogEntry.Call(ctx, app, DeleteLogEntryParams{LogID: "log-1", EntryID: "entry-2"}); err != nil {
		t.Fatal(err)
	}
	if store.writeUserID != "user-1" || store.writeEntryID != "entry-2" {
		t.Fatalf("DeleteLogEntry store call = (%q, %q)", store.writeUserID, store.writeEntryID)
	}
}

// Entry values are checked against the definitions the store returns for this
// user and log — never against anything the caller supplied. A caller cannot
// widen its own schema by sending extra fields.
func TestLogEntryValuesAreValidatedAgainstStoredDefinitions(t *testing.T) {
	store := &fakeLogEntryStore{definitions: doseDefinitions()}
	app := New(Config{Entries: store})
	ctx := WithUserID(context.Background(), "user-1")
	occurred := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)

	rejected := []struct {
		name   string
		fields map[string]any
	}{
		{"unknown field", map[string]any{"dose": "500", "secret": "x"}},
		{"missing required field", map[string]any{"notes": "after lunch"}},
		{"wrong type for number", map[string]any{"dose": "not-a-number"}},
		{"wrong type for text", map[string]any{"dose": "500", "notes": 42}},
	}
	for _, c := range rejected {
		var validationErr *ValidationError
		if _, err := CreateLogEntry.Call(ctx, app, CreateLogEntryParams{LogID: "log-1", Fields: c.fields}); !errors.As(err, &validationErr) {
			t.Fatalf("create_log_entry %s: error = %T %v, want ValidationError", c.name, err, err)
		}
		update := UpdateLogEntryParams{LogID: "log-1", EntryID: "entry-1", Fields: c.fields, OccurredAt: occurred}
		if _, err := UpdateLogEntry.Call(ctx, app, update); !errors.As(err, &validationErr) {
			t.Fatalf("update_log_entry %s: error = %T %v, want ValidationError", c.name, err, err)
		}
	}
	if store.writeCalls != 0 {
		t.Fatalf("invalid entries reached the store %d times", store.writeCalls)
	}
	if store.definitionsUserID != "user-1" || store.definitionsLogID != "log-1" {
		t.Fatalf("definitions were fetched for (%q, %q)", store.definitionsUserID, store.definitionsLogID)
	}
}

// The definition lookup is the visibility check: a log the caller cannot see
// reports ErrLogNotFound, and no write may follow.
func TestLogEntryWritesStopWhenDefinitionsAreUnavailable(t *testing.T) {
	store := &fakeLogEntryStore{definitionsErr: ErrLogNotFound}
	app := New(Config{Entries: store})
	ctx := WithUserID(context.Background(), "user-1")

	if _, err := CreateLogEntry.Call(ctx, app, CreateLogEntryParams{LogID: "log-1"}); !errors.Is(err, ErrLogNotFound) {
		t.Fatalf("create_log_entry error = %v, want ErrLogNotFound", err)
	}
	update := UpdateLogEntryParams{LogID: "log-1", EntryID: "entry-1", OccurredAt: time.Now()}
	if _, err := UpdateLogEntry.Call(ctx, app, update); !errors.Is(err, ErrLogNotFound) {
		t.Fatalf("update_log_entry error = %v, want ErrLogNotFound", err)
	}
	if store.writeCalls != 0 {
		t.Fatalf("writes proceeded past an unreadable log %d times", store.writeCalls)
	}
}

// An entry in a log with no custom fields must reach the store as an empty
// map, not nil, so it serializes to a JSONB object rather than null.
func TestCreateLogEntryNormalizesNilFields(t *testing.T) {
	store := &fakeLogEntryStore{}
	app := New(Config{Entries: store})
	ctx := WithUserID(context.Background(), "user-1")

	if _, err := CreateLogEntry.Call(ctx, app, CreateLogEntryParams{LogID: "log-1"}); err != nil {
		t.Fatal(err)
	}
	if store.writeFields == nil || len(store.writeFields) != 0 {
		t.Fatalf("stored fields = %#v, want an empty map", store.writeFields)
	}
}

func TestUpdateLogEntryRequiresOccurredAt(t *testing.T) {
	store := &fakeLogEntryStore{definitions: doseDefinitions()}
	app := New(Config{Entries: store})
	ctx := WithUserID(context.Background(), "user-1")

	var validationErr *ValidationError
	update := UpdateLogEntryParams{LogID: "log-1", EntryID: "entry-1", Fields: map[string]any{"dose": "500"}}
	if _, err := UpdateLogEntry.Call(ctx, app, update); !errors.As(err, &validationErr) {
		t.Fatalf("update_log_entry error = %T %v, want ValidationError", err, err)
	}
	if store.definitionCalls != 0 || store.writeCalls != 0 {
		t.Fatalf("params validation ran after the store was called (%d, %d)", store.definitionCalls, store.writeCalls)
	}
}

func TestLogEntryActionsRequireAuthenticationBeforeCallingStore(t *testing.T) {
	store := &fakeLogEntryStore{}
	app := New(Config{Entries: store})
	ctx := context.Background()

	calls := []struct {
		name string
		call func() error
	}{
		{"create_log_entry", func() error {
			_, e := CreateLogEntry.Call(ctx, app, CreateLogEntryParams{LogID: "log-1"})
			return e
		}},
		{"list_log_entries", func() error {
			_, e := ListLogEntries.Call(ctx, app, ListLogEntriesParams{LogID: "log-1"})
			return e
		}},
		{"update_log_entry", func() error {
			_, e := UpdateLogEntry.Call(ctx, app, UpdateLogEntryParams{LogID: "log-1", EntryID: "e-1", OccurredAt: time.Now()})
			return e
		}},
		{"delete_log_entry", func() error {
			_, e := DeleteLogEntry.Call(ctx, app, DeleteLogEntryParams{LogID: "log-1", EntryID: "e-1"})
			return e
		}},
	}
	for _, c := range calls {
		if err := c.call(); !errors.Is(err, ErrUnauthenticated) {
			t.Fatalf("%s error = %v, want ErrUnauthenticated", c.name, err)
		}
	}
	if store.definitionCalls != 0 || store.writeCalls != 0 {
		t.Fatalf("unauthenticated calls reached the store (%d, %d)", store.definitionCalls, store.writeCalls)
	}
}

func TestLogEntryActionsPropagateStoreSentinels(t *testing.T) {
	store := &fakeLogEntryStore{definitions: doseDefinitions(), err: ErrLogEntryNotFound}
	app := New(Config{Entries: store})
	ctx := WithUserID(context.Background(), "user-1")

	update := UpdateLogEntryParams{LogID: "log-1", EntryID: "entry-1", Fields: map[string]any{"dose": "500"}, OccurredAt: time.Now()}
	if _, err := UpdateLogEntry.Call(ctx, app, update); !errors.Is(err, ErrLogEntryNotFound) {
		t.Fatalf("update_log_entry error = %v, want ErrLogEntryNotFound", err)
	}
	if _, err := DeleteLogEntry.Call(ctx, app, DeleteLogEntryParams{LogID: "log-1", EntryID: "entry-1"}); !errors.Is(err, ErrLogEntryNotFound) {
		t.Fatalf("delete_log_entry error = %v, want ErrLogEntryNotFound", err)
	}
}
