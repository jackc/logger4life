package jedstore

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/logger4life/backend/core"
	"github.com/stretchr/testify/require"
)

func TestWriteReceiptsSurviveReopenAndRollback(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(dir)
	require.NoError(t, err)
	app := core.New(core.Config{Tx: store, Writes: store, Users: store, Sessions: store, Folders: store})
	u, err := core.RegisterUser.Call(context.Background(), app, core.RegisterUserParams{Username: "writer", Password: "password123"})
	require.NoError(t, err)
	ctx := core.WithUserID(context.Background(), u.User.ID)
	input := core.CreateFolderParams{Name: "Persistent"}
	create := func(ctx context.Context) (core.Folder, error) { return core.CreateFolder.Call(ctx, app, input) }
	first, err := core.RunWrite(ctx, app, "create_folder", "persistent", input, create)
	require.NoError(t, err)
	require.NoError(t, store.Close())
	store, err = Open(dir)
	require.NoError(t, err)
	defer store.Close()
	app = core.New(core.Config{Tx: store, Writes: store, Folders: store})
	replay, err := core.RunWrite(ctx, app, "create_folder", "persistent", input, func(context.Context) (core.Folder, error) {
		t.Fatal("replayed mutation after reopening")
		return core.Folder{}, nil
	})
	require.NoError(t, err)
	require.Equal(t, first, replay)
	_, err = core.RunWrite(ctx, app, "create_folder", "persistent", core.CreateFolderParams{Name: "Different"}, create)
	require.ErrorIs(t, err, core.ErrIdempotencyConflict)
	_, err = core.RunWrite(ctx, app, "create_folder", strings.Repeat("x", 129), input, create)
	var validation *core.ValidationError
	require.ErrorAs(t, err, &validation)
	failure := errors.New("readback failed")
	_, err = core.RunWrite(ctx, app, "create_folder", "rollback", input, func(ctx context.Context) (core.Folder, error) {
		v, e := create(ctx)
		if e != nil {
			return v, e
		}
		return v, failure
	})
	require.ErrorIs(t, err, failure)
	folders, err := core.ListFolders.Call(ctx, app, core.ListFoldersParams{})
	require.NoError(t, err)
	require.Len(t, folders, 1)
	_, err = core.RunWrite(ctx, app, "create_folder", "rollback", input, create)
	require.NoError(t, err)
	folders, err = core.ListFolders.Call(ctx, app, core.ListFoldersParams{})
	require.NoError(t, err)
	require.Len(t, folders, 2)
}
