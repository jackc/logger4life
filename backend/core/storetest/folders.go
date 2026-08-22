package storetest

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/logger4life/backend/core"
)

// RunFolderStore checks the port behind the tree users organize their logs
// into. Most of its contract is about refusing to build a tree that cannot be
// walked — a folder inside itself, or inside its own descendant.
func RunFolderStore(t *testing.T, ports Ports) {
	ctx := context.Background()

	t.Run("round trips a folder and its parent", func(t *testing.T) {
		owner := newUser(t, ports)
		parent, err := ports.CreateFolder(ctx, owner.ID, "Health", nil)
		if err != nil {
			t.Fatal(err)
		}
		if parent.Name != "Health" || parent.ParentFolderID != nil {
			t.Errorf("created %#v, want a folder at the root", parent)
		}

		child, err := ports.CreateFolder(ctx, owner.ID, "Vitamins", &parent.ID)
		if err != nil {
			t.Fatal(err)
		}
		if child.ParentFolderID == nil || *child.ParentFolderID != parent.ID {
			t.Errorf("child parent = %v, want %s", child.ParentFolderID, parent.ID)
		}
	})

	t.Run("lists a user's own folders and nothing else", func(t *testing.T) {
		owner := newUser(t, ports)
		empty, err := ports.ListFolders(ctx, owner.ID)
		if err != nil {
			t.Fatal(err)
		}
		if empty == nil {
			t.Error("ListFolders returned nil for a user with no folders")
		}

		mine, err := ports.CreateFolder(ctx, owner.ID, "Health", nil)
		if err != nil {
			t.Fatal(err)
		}
		stranger := newUser(t, ports)
		if _, err := ports.CreateFolder(ctx, stranger.ID, "Theirs", nil); err != nil {
			t.Fatal(err)
		}

		listed, err := ports.ListFolders(ctx, owner.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(listed) != 1 || listed[0].ID != mine.ID {
			t.Errorf("ListFolders = %#v, want only the caller's folder", listed)
		}
	})

	// A parent belonging to somebody else is reported as missing rather than
	// forbidden, so the caller learns nothing about whether the ID was real.
	t.Run("hides another user's folder from every method", func(t *testing.T) {
		owner := newUser(t, ports)
		stranger := newUser(t, ports)
		folder, err := ports.CreateFolder(ctx, owner.ID, "Private", nil)
		if err != nil {
			t.Fatal(err)
		}

		if _, err := ports.RenameFolder(ctx, stranger.ID, folder.ID, "Hijacked"); !errors.Is(err, core.ErrFolderNotFound) {
			t.Errorf("RenameFolder as a stranger = %v, want ErrFolderNotFound", err)
		}
		if err := ports.MoveFolder(ctx, stranger.ID, folder.ID, nil, 0); !errors.Is(err, core.ErrFolderNotFound) {
			t.Errorf("MoveFolder as a stranger = %v, want ErrFolderNotFound", err)
		}
		if err := ports.DeleteFolder(ctx, stranger.ID, folder.ID); !errors.Is(err, core.ErrFolderNotFound) {
			t.Errorf("DeleteFolder as a stranger = %v, want ErrFolderNotFound", err)
		}
		if _, err := ports.CreateFolder(ctx, stranger.ID, "Child", &folder.ID); !errors.Is(err, core.ErrParentFolderNotFound) {
			t.Errorf("CreateFolder under a stranger's folder = %v, want ErrParentFolderNotFound", err)
		}

		survivors, err := ports.ListFolders(ctx, owner.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(survivors) != 1 || survivors[0].Name != "Private" {
			t.Errorf("the owner's folder did not survive a stranger's writes: %#v", survivors)
		}
	})

	t.Run("reports an unknown folder rather than a zero one", func(t *testing.T) {
		owner := newUser(t, ports)
		if _, err := ports.RenameFolder(ctx, owner.ID, UnknownID, "Name"); !errors.Is(err, core.ErrFolderNotFound) {
			t.Errorf("RenameFolder error = %v, want ErrFolderNotFound", err)
		}
		if err := ports.MoveFolder(ctx, owner.ID, UnknownID, nil, 0); !errors.Is(err, core.ErrFolderNotFound) {
			t.Errorf("MoveFolder error = %v, want ErrFolderNotFound", err)
		}
		if err := ports.DeleteFolder(ctx, owner.ID, UnknownID); !errors.Is(err, core.ErrFolderNotFound) {
			t.Errorf("DeleteFolder error = %v, want ErrFolderNotFound", err)
		}
		if _, err := ports.CreateFolder(ctx, owner.ID, "Child", ptr(UnknownID)); !errors.Is(err, core.ErrParentFolderNotFound) {
			t.Errorf("CreateFolder under an unknown parent = %v, want ErrParentFolderNotFound", err)
		}
	})

	t.Run("renames a folder", func(t *testing.T) {
		owner := newUser(t, ports)
		folder, err := ports.CreateFolder(ctx, owner.ID, "Helth", nil)
		if err != nil {
			t.Fatal(err)
		}
		renamed, err := ports.RenameFolder(ctx, owner.ID, folder.ID, "Health")
		if err != nil {
			t.Fatal(err)
		}
		if renamed.ID != folder.ID || renamed.Name != "Health" {
			t.Errorf("renamed = %#v, want the same folder under the new name", renamed)
		}
	})

	// A tree that contains a loop cannot be rendered or walked, so the two
	// ways of making one are refused with distinct sentinels: the caller is
	// told which mistake it made.
	t.Run("refuses to make a folder its own ancestor", func(t *testing.T) {
		owner := newUser(t, ports)
		grandparent, err := ports.CreateFolder(ctx, owner.ID, "Grandparent", nil)
		if err != nil {
			t.Fatal(err)
		}
		parent, err := ports.CreateFolder(ctx, owner.ID, "Parent", &grandparent.ID)
		if err != nil {
			t.Fatal(err)
		}
		child, err := ports.CreateFolder(ctx, owner.ID, "Child", &parent.ID)
		if err != nil {
			t.Fatal(err)
		}

		if err := ports.MoveFolder(ctx, owner.ID, grandparent.ID, &grandparent.ID, 0); !errors.Is(err, core.ErrFolderOwnParent) {
			t.Errorf("moving a folder into itself = %v, want ErrFolderOwnParent", err)
		}
		if err := ports.MoveFolder(ctx, owner.ID, grandparent.ID, &child.ID, 0); !errors.Is(err, core.ErrFolderCycle) {
			t.Errorf("moving a folder into its own descendant = %v, want ErrFolderCycle", err)
		}
		// Moving the other way round is the legitimate case and must work.
		if err := ports.MoveFolder(ctx, owner.ID, child.ID, &grandparent.ID, 0); err != nil {
			t.Errorf("moving a folder up to its grandparent = %v, want it allowed", err)
		}
	})

	// Deleting a folder must not orphan whatever it holds, so a folder with
	// children of either kind is refused until it is emptied.
	t.Run("refuses to delete a folder that still holds something", func(t *testing.T) {
		owner := newUser(t, ports)
		folder, err := ports.CreateFolder(ctx, owner.ID, "Health", nil)
		if err != nil {
			t.Fatal(err)
		}
		child, err := ports.CreateFolder(ctx, owner.ID, "Vitamins", &folder.ID)
		if err != nil {
			t.Fatal(err)
		}
		if err := ports.DeleteFolder(ctx, owner.ID, folder.ID); !errors.Is(err, core.ErrFolderNotEmpty) {
			t.Errorf("deleting a folder holding a folder = %v, want ErrFolderNotEmpty", err)
		}

		// Empty it of folders, then put a log in it instead.
		if err := ports.DeleteFolder(ctx, owner.ID, child.ID); err != nil {
			t.Fatal(err)
		}
		log := newLog(t, ports, owner.ID, "Vitamins")
		if err := ports.UpdateLogPlacement(ctx, owner.ID, log.ID, domainPlacement(folder.ID, 0)); err != nil {
			t.Fatal(err)
		}
		if err := ports.DeleteFolder(ctx, owner.ID, folder.ID); !errors.Is(err, core.ErrFolderNotEmpty) {
			t.Errorf("deleting a folder holding a log = %v, want ErrFolderNotEmpty", err)
		}

		if err := ports.UpdateLogPlacement(ctx, owner.ID, log.ID, domainPlacement("", 0)); err != nil {
			t.Fatal(err)
		}
		if err := ports.DeleteFolder(ctx, owner.ID, folder.ID); err != nil {
			t.Errorf("deleting an emptied folder = %v, want it allowed", err)
		}
	})

	// Positions are a dense sequence per parent, which is what lets the client
	// send a target index and get the order it expected.
	t.Run("keeps sibling positions dense as folders move and go", func(t *testing.T) {
		owner := newUser(t, ports)
		var ids []string
		for _, name := range []string{"First", "Second", "Third", "Fourth"} {
			f, err := ports.CreateFolder(ctx, owner.ID, name, nil)
			if err != nil {
				t.Fatal(err)
			}
			ids = append(ids, f.ID)
		}
		assertDenseFolders(t, ports, owner.ID)

		// Move the last to the front, then delete one from the middle.
		if err := ports.MoveFolder(ctx, owner.ID, ids[3], nil, 0); err != nil {
			t.Fatal(err)
		}
		assertDenseFolders(t, ports, owner.ID)
		if got := folderNames(t, ports, owner.ID); got[0] != "Fourth" {
			t.Errorf("order after the move = %v, want Fourth first", got)
		}

		if err := ports.DeleteFolder(ctx, owner.ID, ids[1]); err != nil {
			t.Fatal(err)
		}
		assertDenseFolders(t, ports, owner.ID)
	})
}

func ptr[T any](v T) *T { return &v }

// assertDenseFolders checks that every root folder holds a distinct position
// running from zero, which a gap or a repeat would break.
func assertDenseFolders(t *testing.T, ports Ports, userID string) {
	t.Helper()
	folders, err := ports.ListFolders(context.Background(), userID)
	if err != nil {
		t.Fatal(err)
	}
	want := 0
	for _, f := range folders {
		if f.ParentFolderID != nil {
			continue
		}
		if f.Position != want {
			t.Errorf("folder %q sits at position %d, want %d; the sequence has a gap or a repeat", f.Name, f.Position, want)
		}
		want++
	}
}

func folderNames(t *testing.T, ports Ports, userID string) []string {
	t.Helper()
	folders, err := ports.ListFolders(context.Background(), userID)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range folders {
		if f.ParentFolderID == nil {
			names = append(names, f.Name)
		}
	}
	return names
}
