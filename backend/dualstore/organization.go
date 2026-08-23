package dualstore

import (
	"context"

	"github.com/jackc/logger4life/backend/core"
	"github.com/jackc/logger4life/backend/domain"
)

func (s *Store) UpdateLogPlacement(ctx context.Context, userID, logID string, change domain.LogPlacementChange) error {
	return compareError("UpdateLogPlacement",
		func() error { return s.primary.UpdateLogPlacement(ctx, userID, logID, change) },
		func() error { return s.secondary.UpdateLogPlacement(ctx, userID, logID, change) })
}

func (s *Store) PinLog(ctx context.Context, userID, logID string, change domain.HomePinChange) error {
	return compareError("PinLog",
		func() error { return s.primary.PinLog(ctx, userID, logID, change) },
		func() error { return s.secondary.PinLog(ctx, userID, logID, change) })
}

func (s *Store) UpdateLogHomePosition(ctx context.Context, userID, logID string, change domain.HomeOrderChange) error {
	return compareError("UpdateLogHomePosition",
		func() error { return s.primary.UpdateLogHomePosition(ctx, userID, logID, change) },
		func() error { return s.secondary.UpdateLogHomePosition(ctx, userID, logID, change) })
}

func (s *Store) CreateFolder(ctx context.Context, id, userID, name string, parent *string) (core.Folder, error) {
	return compareCall("CreateFolder",
		func() (core.Folder, error) { return s.primary.CreateFolder(ctx, id, userID, name, parent) },
		func() (core.Folder, error) { return s.secondary.CreateFolder(ctx, id, userID, name, parent) })
}

func (s *Store) ListFolders(ctx context.Context, userID string) ([]core.Folder, error) {
	return compareCall("ListFolders",
		func() ([]core.Folder, error) { return s.primary.ListFolders(ctx, userID) },
		func() ([]core.Folder, error) { return s.secondary.ListFolders(ctx, userID) })
}

func (s *Store) RenameFolder(ctx context.Context, userID, folderID, name string) (core.Folder, error) {
	return compareCall("RenameFolder",
		func() (core.Folder, error) { return s.primary.RenameFolder(ctx, userID, folderID, name) },
		func() (core.Folder, error) { return s.secondary.RenameFolder(ctx, userID, folderID, name) })
}

func (s *Store) MoveFolder(ctx context.Context, userID, folderID string, parent *string, position int) error {
	return compareError("MoveFolder",
		func() error { return s.primary.MoveFolder(ctx, userID, folderID, parent, position) },
		func() error { return s.secondary.MoveFolder(ctx, userID, folderID, parent, position) })
}

func (s *Store) DeleteFolder(ctx context.Context, userID, folderID string) error {
	return compareError("DeleteFolder",
		func() error { return s.primary.DeleteFolder(ctx, userID, folderID) },
		func() error { return s.secondary.DeleteFolder(ctx, userID, folderID) })
}
