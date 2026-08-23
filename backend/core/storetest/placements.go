package storetest

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/logger4life/backend/core"
	"github.com/jackc/logger4life/backend/domain"
)

// domainPlacement builds a placement change, treating an empty folder ID as
// the root so callers can express both cases in one line.
func domainPlacement(folderID string, position int) domain.LogPlacementChange {
	change := domain.LogPlacementChange{Position: position}
	if folderID != "" {
		change.FolderID = &folderID
	}
	return change
}

// RunLogPlacementStore checks the port that records where each user keeps a
// log. Placement is per user rather than per log: a shared log sits wherever
// each member filed it, so the same log has one position for its owner and
// another for everyone it was shared with.
func RunLogPlacementStore(t *testing.T, ports Ports) {
	ctx := context.Background()

	t.Run("moves a log into a folder and back to the root", func(t *testing.T) {
		owner := newUser(t, ports)
		folder, err := ports.CreateFolder(ctx, newRowID(), owner.ID, "Health", nil)
		if err != nil {
			t.Fatal(err)
		}
		log := newLog(t, ports, owner.ID, "Vitamins")

		if err := ports.UpdateLogPlacement(ctx, owner.ID, log.ID, domainPlacement(folder.ID, 0)); err != nil {
			t.Fatal(err)
		}
		placed, err := ports.GetLog(ctx, owner.ID, log.ID)
		if err != nil {
			t.Fatal(err)
		}
		if placed.FolderID == nil || *placed.FolderID != folder.ID {
			t.Errorf("FolderID = %v, want %s", placed.FolderID, folder.ID)
		}

		// A nil folder is the root, not "leave it alone".
		if err := ports.UpdateLogPlacement(ctx, owner.ID, log.ID, domainPlacement("", 0)); err != nil {
			t.Fatal(err)
		}
		rooted, err := ports.GetLog(ctx, owner.ID, log.ID)
		if err != nil {
			t.Fatal(err)
		}
		if rooted.FolderID != nil {
			t.Errorf("FolderID = %v after moving to the root, want nil", rooted.FolderID)
		}
	})

	t.Run("hides another user's placement from every method", func(t *testing.T) {
		owner := newUser(t, ports)
		stranger := newUser(t, ports)
		log := newLog(t, ports, owner.ID, "Private")

		if err := ports.UpdateLogPlacement(ctx, stranger.ID, log.ID, domainPlacement("", 0)); !errors.Is(err, core.ErrLogNotFound) {
			t.Errorf("UpdateLogPlacement as a stranger = %v, want ErrLogNotFound", err)
		}
		if err := ports.PinLog(ctx, stranger.ID, log.ID, domain.HomePinChange{Pinned: false}); !errors.Is(err, core.ErrLogNotFound) {
			t.Errorf("PinLog as a stranger = %v, want ErrLogNotFound", err)
		}
		if err := ports.UpdateLogHomePosition(ctx, stranger.ID, log.ID, domain.HomeOrderChange{HomePosition: 0}); !errors.Is(err, core.ErrLogNotFound) {
			t.Errorf("UpdateLogHomePosition as a stranger = %v, want ErrLogNotFound", err)
		}
	})

	// A folder the caller does not own is no more usable as a destination than
	// one that does not exist.
	t.Run("refuses to file a log into a folder the caller does not own", func(t *testing.T) {
		owner := newUser(t, ports)
		stranger := newUser(t, ports)
		theirFolder, err := ports.CreateFolder(ctx, newRowID(), stranger.ID, "Theirs", nil)
		if err != nil {
			t.Fatal(err)
		}
		log := newLog(t, ports, owner.ID, "Vitamins")

		if err := ports.UpdateLogPlacement(ctx, owner.ID, log.ID, domainPlacement(theirFolder.ID, 0)); !errors.Is(err, core.ErrFolderNotFound) {
			t.Errorf("filing into a stranger's folder = %v, want ErrFolderNotFound", err)
		}
		if err := ports.UpdateLogPlacement(ctx, owner.ID, log.ID, domainPlacement(UnknownID, 0)); !errors.Is(err, core.ErrFolderNotFound) {
			t.Errorf("filing into an unknown folder = %v, want ErrFolderNotFound", err)
		}
	})

	t.Run("reports an unknown log rather than doing nothing quietly", func(t *testing.T) {
		owner := newUser(t, ports)
		if err := ports.UpdateLogPlacement(ctx, owner.ID, UnknownID, domainPlacement("", 0)); !errors.Is(err, core.ErrLogNotFound) {
			t.Errorf("UpdateLogPlacement error = %v, want ErrLogNotFound", err)
		}
		if err := ports.PinLog(ctx, owner.ID, UnknownID, domain.HomePinChange{Pinned: true}); !errors.Is(err, core.ErrLogNotFound) {
			t.Errorf("PinLog error = %v, want ErrLogNotFound", err)
		}
		if err := ports.UpdateLogHomePosition(ctx, owner.ID, UnknownID, domain.HomeOrderChange{}); !errors.Is(err, core.ErrLogNotFound) {
			t.Errorf("UpdateLogHomePosition error = %v, want ErrLogNotFound", err)
		}
	})

	t.Run("pins and unpins a log", func(t *testing.T) {
		owner := newUser(t, ports)
		log := newLog(t, ports, owner.ID, "Vitamins")
		if !log.PinnedToHome {
			t.Fatal("a new log is expected to start pinned to home")
		}

		if err := ports.PinLog(ctx, owner.ID, log.ID, domain.HomePinChange{Pinned: false}); err != nil {
			t.Fatal(err)
		}
		unpinned, err := ports.GetLog(ctx, owner.ID, log.ID)
		if err != nil || unpinned.PinnedToHome {
			t.Errorf("PinnedToHome = %v, %v; want it unpinned", unpinned.PinnedToHome, err)
		}

		// Pinning what is already pinned must be a no-op rather than an error,
		// because the client sends the state it wants, not a toggle.
		if err := ports.PinLog(ctx, owner.ID, log.ID, domain.HomePinChange{Pinned: false}); err != nil {
			t.Errorf("unpinning an unpinned log = %v, want nil", err)
		}
		if err := ports.PinLog(ctx, owner.ID, log.ID, domain.HomePinChange{Pinned: true}); err != nil {
			t.Fatal(err)
		}
		repinned, err := ports.GetLog(ctx, owner.ID, log.ID)
		if err != nil || !repinned.PinnedToHome {
			t.Errorf("PinnedToHome = %v, %v; want it pinned again", repinned.PinnedToHome, err)
		}
	})

	// Home order only means something for a log that is on the home screen, so
	// ordering an unpinned one is refused with its own sentinel rather than
	// silently recorded.
	t.Run("refuses to order a log that is not pinned", func(t *testing.T) {
		owner := newUser(t, ports)
		log := newLog(t, ports, owner.ID, "Vitamins")
		if err := ports.PinLog(ctx, owner.ID, log.ID, domain.HomePinChange{Pinned: false}); err != nil {
			t.Fatal(err)
		}
		if err := ports.UpdateLogHomePosition(ctx, owner.ID, log.ID, domain.HomeOrderChange{HomePosition: 0}); !errors.Is(err, core.ErrLogNotPinned) {
			t.Errorf("ordering an unpinned log = %v, want ErrLogNotPinned", err)
		}
	})

	// The client sends a target index from a drag, so an index past the end
	// means "last" rather than an error, and the result stays dense.
	t.Run("keeps home positions dense and clamps an index past the end", func(t *testing.T) {
		owner := newUser(t, ports)
		var ids []string
		for _, name := range []string{"First", "Second", "Third"} {
			ids = append(ids, newLog(t, ports, owner.ID, name).ID)
		}
		assertDenseHome(t, ports, owner.ID)

		if err := ports.UpdateLogHomePosition(ctx, owner.ID, ids[0], domain.HomeOrderChange{HomePosition: 99}); err != nil {
			t.Fatal(err)
		}
		assertDenseHome(t, ports, owner.ID)
		moved, err := ports.GetLog(ctx, owner.ID, ids[0])
		if err != nil {
			t.Fatal(err)
		}
		if moved.HomePosition != 2 {
			t.Errorf("HomePosition = %d after asking for 99, want it clamped to the last slot 2", moved.HomePosition)
		}

		if err := ports.UpdateLogHomePosition(ctx, owner.ID, ids[0], domain.HomeOrderChange{HomePosition: 0}); err != nil {
			t.Fatal(err)
		}
		assertDenseHome(t, ports, owner.ID)
	})

	// The same guarantee for the folder view, and unpinning must not leave a
	// hole in the home sequence for the logs that are still pinned.
	t.Run("keeps folder positions dense as logs move", func(t *testing.T) {
		owner := newUser(t, ports)
		var ids []string
		for _, name := range []string{"First", "Second", "Third"} {
			ids = append(ids, newLog(t, ports, owner.ID, name).ID)
		}

		if err := ports.UpdateLogPlacement(ctx, owner.ID, ids[2], domainPlacement("", 0)); err != nil {
			t.Fatal(err)
		}
		assertDenseRootPositions(t, ports, owner.ID)

		folder, err := ports.CreateFolder(ctx, newRowID(), owner.ID, "Health", nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := ports.UpdateLogPlacement(ctx, owner.ID, ids[1], domainPlacement(folder.ID, 0)); err != nil {
			t.Fatal(err)
		}
		// Moving a log out of the root must close the gap it left behind.
		assertDenseRootPositions(t, ports, owner.ID)
	})
}

func assertDenseHome(t *testing.T, ports Ports, userID string) {
	t.Helper()
	logs, err := ports.ListLogs(context.Background(), userID)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[int]string{}
	for _, l := range logs {
		if !l.PinnedToHome {
			continue
		}
		if other, clash := seen[l.HomePosition]; clash {
			t.Errorf("%q and %q both sit at home position %d", other, l.Name, l.HomePosition)
		}
		seen[l.HomePosition] = l.Name
	}
	for i := range len(seen) {
		if _, ok := seen[i]; !ok {
			t.Errorf("home position %d is empty while %d logs are pinned; the sequence has a gap", i, len(seen))
		}
	}
}

func assertDenseRootPositions(t *testing.T, ports Ports, userID string) {
	t.Helper()
	logs, err := ports.ListLogs(context.Background(), userID)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[int]string{}
	for _, l := range logs {
		if l.FolderID != nil {
			continue
		}
		if other, clash := seen[l.Position]; clash {
			t.Errorf("%q and %q both sit at root position %d", other, l.Name, l.Position)
		}
		seen[l.Position] = l.Name
	}
	for i := range len(seen) {
		if _, ok := seen[i]; !ok {
			t.Errorf("root position %d is empty while %d logs sit there; the sequence has a gap", i, len(seen))
		}
	}
}
