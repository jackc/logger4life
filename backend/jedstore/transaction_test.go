package jedstore

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/logger4life/backend/core"
)

func TestStoreHonorsAmbientTransaction(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("closing jed store: %v", err)
		}
	})
	ctx := context.Background()

	write := func(ctx context.Context) (string, error) {
		user, err := store.CreateUser(ctx, "00000000-0000-4000-8000-000000000201", "tx_probe_user", nil, "hash")
		if err != nil {
			return "", err
		}
		if _, err := store.CreateLog(ctx, "00000000-0000-7000-8000-000000000202", user.ID, "tx_probe_log", nil); err != nil {
			return "", err
		}
		if _, err := store.CreateFolder(ctx, "00000000-0000-7000-8000-000000000203", user.ID, "tx_probe_folder", nil); err != nil {
			return "", err
		}
		return user.ID, nil
	}

	rollback := errors.New("rolled back on purpose")
	err = store.InTx(ctx, func(ctx context.Context) error {
		if _, err := write(ctx); err != nil {
			return err
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatalf("err = %v, want the sentinel the closure returned", err)
	}
	if _, _, err := store.GetUserByUsername(ctx, "tx_probe_user"); !errors.Is(err, core.ErrUserNotFound) {
		t.Fatalf("user after rollback = %v, want ErrUserNotFound", err)
	}

	var userID string
	if err := store.InTx(ctx, func(ctx context.Context) error {
		var err error
		userID, err = write(ctx)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	logs, err := store.ListLogs(ctx, userID)
	if err != nil || len(logs) != 1 {
		t.Fatalf("logs after commit = %#v, %v; want one", logs, err)
	}
	folders, err := store.ListFolders(ctx, userID)
	if err != nil || len(folders) != 1 {
		t.Fatalf("folders after commit = %#v, %v; want one", folders, err)
	}
}
