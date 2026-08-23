package core

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeFolderStore struct {
	scopeUserID string
	folderID    string
	name        string
	parentID    *string
	position    int
	folder      Folder
	folders     []Folder
	err         error
	calls       int
}

func (s *fakeFolderStore) CreateFolder(_ context.Context, _ string, userID, name string, parentID *string) (Folder, error) {
	s.calls++
	s.scopeUserID, s.name, s.parentID = userID, name, parentID
	return s.folder, s.err
}

func (s *fakeFolderStore) ListFolders(_ context.Context, userID string) ([]Folder, error) {
	s.calls++
	s.scopeUserID = userID
	return s.folders, s.err
}

func (s *fakeFolderStore) RenameFolder(_ context.Context, userID, folderID, name string) (Folder, error) {
	s.calls++
	s.scopeUserID, s.folderID, s.name = userID, folderID, name
	return s.folder, s.err
}

func (s *fakeFolderStore) MoveFolder(_ context.Context, userID, folderID string, parentID *string, position int) error {
	s.calls++
	s.scopeUserID, s.folderID, s.parentID, s.position = userID, folderID, parentID, position
	return s.err
}

func (s *fakeFolderStore) DeleteFolder(_ context.Context, userID, folderID string) error {
	s.calls++
	s.scopeUserID, s.folderID = userID, folderID
	return s.err
}

func TestFolderActionsScopeStoreCallsToContextUser(t *testing.T) {
	store := &fakeFolderStore{folder: Folder{ID: testID("folder-1")}, folders: []Folder{{ID: testID("folder-1")}}}
	app := New(Config{Folders: store})
	ctx := WithUserID(context.Background(), "user-1")
	parent := testID("folder-parent")

	if _, err := CreateFolder.Call(ctx, app, CreateFolderParams{Name: "Health", ParentFolderID: &parent}); err != nil {
		t.Fatal(err)
	}
	if store.scopeUserID != "user-1" || store.name != "Health" || store.parentID != &parent {
		t.Fatalf("CreateFolder store call = (%q, %q, %v)", store.scopeUserID, store.name, store.parentID)
	}

	folders, err := ListFolders.Call(ctx, app, ListFoldersParams{})
	if err != nil || len(folders) != 1 || folders[0].ID != testID("folder-1") {
		t.Fatalf("ListFolders() = %#v, %v", folders, err)
	}

	if _, err := RenameFolder.Call(ctx, app, RenameFolderParams{FolderID: testID("folder-1"), Name: "Fitness"}); err != nil {
		t.Fatal(err)
	}
	if store.scopeUserID != "user-1" || store.folderID != testID("folder-1") || store.name != "Fitness" {
		t.Fatalf("RenameFolder store call = (%q, %q, %q)", store.scopeUserID, store.folderID, store.name)
	}

	if _, err := MoveFolder.Call(ctx, app, MoveFolderParams{FolderID: testID("folder-2"), Position: 3}); err != nil {
		t.Fatal(err)
	}
	if store.folderID != testID("folder-2") || store.parentID != nil || store.position != 3 {
		t.Fatalf("MoveFolder store call = (%q, %v, %d)", store.folderID, store.parentID, store.position)
	}

	if _, err := DeleteFolder.Call(ctx, app, DeleteFolderParams{FolderID: testID("folder-3")}); err != nil {
		t.Fatal(err)
	}
	if store.scopeUserID != "user-1" || store.folderID != testID("folder-3") {
		t.Fatalf("DeleteFolder store call = (%q, %q)", store.scopeUserID, store.folderID)
	}
}

func TestFolderActionsRequireAuthenticationBeforeCallingStore(t *testing.T) {
	store := &fakeFolderStore{}
	app := New(Config{Folders: store})
	ctx := context.Background()

	calls := []struct {
		name string
		call func() error
	}{
		{"create_folder", func() error { _, e := CreateFolder.Call(ctx, app, CreateFolderParams{Name: "Health"}); return e }},
		{"list_folders", func() error { _, e := ListFolders.Call(ctx, app, ListFoldersParams{}); return e }},
		{"rename_folder", func() error {
			_, e := RenameFolder.Call(ctx, app, RenameFolderParams{FolderID: testID("f-1"), Name: "Health"})
			return e
		}},
		{"move_folder", func() error { _, e := MoveFolder.Call(ctx, app, MoveFolderParams{FolderID: testID("f-1")}); return e }},
		{"delete_folder", func() error {
			_, e := DeleteFolder.Call(ctx, app, DeleteFolderParams{FolderID: testID("f-1")})
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

func TestFolderNameIsTrimmedAndBounded(t *testing.T) {
	store := &fakeFolderStore{}
	app := New(Config{Folders: store})
	ctx := WithUserID(context.Background(), "user-1")

	if _, err := CreateFolder.Call(ctx, app, CreateFolderParams{Name: "  Health  "}); err != nil {
		t.Fatal(err)
	}
	if store.name != "Health" {
		t.Fatalf("stored name = %q, want the trimmed name", store.name)
	}
	if _, err := RenameFolder.Call(ctx, app, RenameFolderParams{FolderID: testID("f-1"), Name: "  Fitness  "}); err != nil {
		t.Fatal(err)
	}
	if store.name != "Fitness" {
		t.Fatalf("renamed to %q, want the trimmed name", store.name)
	}

	store.calls = 0
	var validationErr *ValidationError
	for _, name := range []string{"", "   ", strings.Repeat("x", 101)} {
		if _, err := CreateFolder.Call(ctx, app, CreateFolderParams{Name: name}); !errors.As(err, &validationErr) {
			t.Fatalf("create_folder %q: error = %T %v, want ValidationError", name, err, err)
		}
		if _, err := RenameFolder.Call(ctx, app, RenameFolderParams{FolderID: testID("f-1"), Name: name}); !errors.As(err, &validationErr) {
			t.Fatalf("rename_folder %q: error = %T %v, want ValidationError", name, err, err)
		}
	}
	if store.calls != 0 {
		t.Fatalf("invalid names reached the store %d times", store.calls)
	}
}

// A negative position is clamped rather than rejected: reordering comes from
// drag-and-drop, where an out-of-range index means "first", not "error".
func TestMoveFolderClampsNegativePosition(t *testing.T) {
	store := &fakeFolderStore{}
	app := New(Config{Folders: store})
	ctx := WithUserID(context.Background(), "user-1")

	if _, err := MoveFolder.Call(ctx, app, MoveFolderParams{FolderID: testID("f-1"), Position: -5}); err != nil {
		t.Fatal(err)
	}
	if store.position != 0 {
		t.Fatalf("stored position = %d, want 0", store.position)
	}
}

func TestFolderActionsPropagateStoreSentinels(t *testing.T) {
	store := &fakeFolderStore{}
	app := New(Config{Folders: store})
	ctx := WithUserID(context.Background(), "user-1")

	for _, sentinel := range []error{ErrFolderNotFound, ErrParentFolderNotFound, ErrFolderCycle, ErrFolderOwnParent} {
		store.err = sentinel
		if _, err := MoveFolder.Call(ctx, app, MoveFolderParams{FolderID: testID("f-1")}); !errors.Is(err, sentinel) {
			t.Fatalf("move_folder error = %v, want %v", err, sentinel)
		}
	}
	store.err = ErrFolderNotEmpty
	if _, err := DeleteFolder.Call(ctx, app, DeleteFolderParams{FolderID: testID("f-1")}); !errors.Is(err, ErrFolderNotEmpty) {
		t.Fatalf("delete_folder error = %v, want ErrFolderNotEmpty", err)
	}
}
