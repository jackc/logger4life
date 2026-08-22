package core

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/logger4life/backend/domain"
)

type fakePlacementStore struct {
	scopeUserID string
	logID       string
	placement   domain.LogPlacementChange
	pin         domain.HomePinChange
	order       domain.HomeOrderChange
	err         error
	calls       int
}

func (s *fakePlacementStore) UpdateLogPlacement(_ context.Context, userID, logID string, change domain.LogPlacementChange) error {
	s.calls++
	s.scopeUserID, s.logID, s.placement = userID, logID, change
	return s.err
}

func (s *fakePlacementStore) PinLog(_ context.Context, userID, logID string, change domain.HomePinChange) error {
	s.calls++
	s.scopeUserID, s.logID, s.pin = userID, logID, change
	return s.err
}

func (s *fakePlacementStore) UpdateLogHomePosition(_ context.Context, userID, logID string, change domain.HomeOrderChange) error {
	s.calls++
	s.scopeUserID, s.logID, s.order = userID, logID, change
	return s.err
}

// Placement params are translated into the domain change types the port
// speaks; the store never sees the wire shape.
func TestPlacementActionsTranslateParamsIntoDomainChanges(t *testing.T) {
	store := &fakePlacementStore{}
	app := New(Config{Placements: store})
	ctx := WithUserID(context.Background(), "user-1")
	folder := "folder-1"

	if _, err := UpdateLogPlacement.Call(ctx, app, UpdateLogPlacementParams{LogID: "log-1", FolderID: &folder, Position: 2}); err != nil {
		t.Fatal(err)
	}
	if store.scopeUserID != "user-1" || store.logID != "log-1" {
		t.Fatalf("UpdateLogPlacement scope = (%q, %q)", store.scopeUserID, store.logID)
	}
	if store.placement.FolderID != &folder || store.placement.Position != 2 {
		t.Fatalf("placement change = %#v", store.placement)
	}

	if _, err := PinLog.Call(ctx, app, PinLogParams{LogID: "log-2", Pinned: true}); err != nil {
		t.Fatal(err)
	}
	if store.logID != "log-2" || !store.pin.Pinned {
		t.Fatalf("pin change = %#v for %q", store.pin, store.logID)
	}

	if _, err := UpdateLogHomePosition.Call(ctx, app, UpdateLogHomePositionParams{LogID: "log-3", HomePosition: 4}); err != nil {
		t.Fatal(err)
	}
	if store.logID != "log-3" || store.order.HomePosition != 4 {
		t.Fatalf("home order change = %#v for %q", store.order, store.logID)
	}
}

// Moving a log to the root of the tree is a nil folder, which must survive the
// translation as nil rather than becoming an empty-string folder ID.
func TestUpdateLogPlacementKeepsRootFolderNil(t *testing.T) {
	store := &fakePlacementStore{}
	app := New(Config{Placements: store})
	ctx := WithUserID(context.Background(), "user-1")

	if _, err := UpdateLogPlacement.Call(ctx, app, UpdateLogPlacementParams{LogID: "log-1"}); err != nil {
		t.Fatal(err)
	}
	if store.placement.FolderID != nil {
		t.Fatalf("root placement folder = %v, want nil", *store.placement.FolderID)
	}
}

func TestPlacementActionsClampNegativePositions(t *testing.T) {
	store := &fakePlacementStore{}
	app := New(Config{Placements: store})
	ctx := WithUserID(context.Background(), "user-1")

	if _, err := UpdateLogPlacement.Call(ctx, app, UpdateLogPlacementParams{LogID: "log-1", Position: -3}); err != nil {
		t.Fatal(err)
	}
	if store.placement.Position != 0 {
		t.Fatalf("placement position = %d, want 0", store.placement.Position)
	}
	if _, err := UpdateLogHomePosition.Call(ctx, app, UpdateLogHomePositionParams{LogID: "log-1", HomePosition: -3}); err != nil {
		t.Fatal(err)
	}
	if store.order.HomePosition != 0 {
		t.Fatalf("home position = %d, want 0", store.order.HomePosition)
	}
}

func TestPlacementActionsRequireAuthenticationBeforeCallingStore(t *testing.T) {
	store := &fakePlacementStore{}
	app := New(Config{Placements: store})
	ctx := context.Background()

	calls := []struct {
		name string
		call func() error
	}{
		{"update_log_placement", func() error {
			_, e := UpdateLogPlacement.Call(ctx, app, UpdateLogPlacementParams{LogID: "log-1"})
			return e
		}},
		{"pin_log", func() error { _, e := PinLog.Call(ctx, app, PinLogParams{LogID: "log-1"}); return e }},
		{"update_log_home_position", func() error {
			_, e := UpdateLogHomePosition.Call(ctx, app, UpdateLogHomePositionParams{LogID: "log-1"})
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

func TestPlacementActionsPropagateStoreSentinels(t *testing.T) {
	store := &fakePlacementStore{err: ErrLogNotFound}
	app := New(Config{Placements: store})
	ctx := WithUserID(context.Background(), "user-1")

	if _, err := UpdateLogPlacement.Call(ctx, app, UpdateLogPlacementParams{LogID: "log-1"}); !errors.Is(err, ErrLogNotFound) {
		t.Fatalf("update_log_placement error = %v, want ErrLogNotFound", err)
	}
	store.err = ErrLogNotPinned
	if _, err := UpdateLogHomePosition.Call(ctx, app, UpdateLogHomePositionParams{LogID: "log-1"}); !errors.Is(err, ErrLogNotPinned) {
		t.Fatalf("update_log_home_position error = %v, want ErrLogNotPinned", err)
	}
}
