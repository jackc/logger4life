package pgstore

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/logger4life/backend/domain"
)

const (
	txProbeUser   = "tx_probe_user"
	txProbeLog    = "tx_probe_log"
	txProbeFolder = "tx_probe_folder"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	return New(acquireTestPool(t))
}

func countRows(t *testing.T, s *Store, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := s.pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestStoreHonorsAmbientTransaction is the guarantee actions rely on when
// they wrap several ports in one InTx: every store call made with the
// transaction's context must join it, so a later failure discards the whole
// unit rather than leaving some ports committed.
func TestStoreHonorsAmbientTransaction(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	ctx := context.Background()
	fields := []domain.FieldDefinition{{Name: "Dose", Type: "number"}}

	write := func(ctx context.Context) (string, error) {
		user, err := store.CreateUser(ctx, txProbeUser, nil, "hash")
		if err != nil {
			return "", err
		}
		if _, err := store.CreateLog(ctx, user.ID, txProbeLog, fields); err != nil {
			return "", err
		}
		if _, err := store.CreateFolder(ctx, user.ID, txProbeFolder, nil); err != nil {
			return "", err
		}
		return user.ID, nil
	}

	rollback := errors.New("rolled back on purpose")
	err := store.InTx(ctx, func(ctx context.Context) error {
		if _, err := write(ctx); err != nil {
			return err
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatalf("err = %v, want the sentinel the closure returned", err)
	}

	for _, c := range []struct {
		what string
		sql  string
		arg  string
	}{
		{"users", `SELECT count(*) FROM users WHERE username = $1`, txProbeUser},
		{"logs", `SELECT count(*) FROM logs WHERE name = $1`, txProbeLog},
		{"folders", `SELECT count(*) FROM folders WHERE name = $1`, txProbeFolder},
		{"placements", `SELECT count(*) FROM user_log_placements p JOIN logs l ON l.id = p.log_id WHERE l.name = $1`, txProbeLog},
	} {
		if n := countRows(t, store, c.sql, c.arg); n != 0 {
			t.Errorf("%s rows after rollback = %d, want 0", c.what, n)
		}
	}

	// The same unit committing must persist every write.
	var userID string
	if err := store.InTx(ctx, func(ctx context.Context) error {
		var err error
		userID, err = write(ctx)
		return err
	}); err != nil {
		t.Fatal(err)
	}

	if n := countRows(t, store, `SELECT count(*) FROM logs WHERE user_id = $1`, userID); n != 1 {
		t.Errorf("logs after commit = %d, want 1", n)
	}
	if n := countRows(t, store, `SELECT count(*) FROM folders WHERE user_id = $1`, userID); n != 1 {
		t.Errorf("folders after commit = %d, want 1", n)
	}
	if n := countRows(t, store, `SELECT count(*) FROM user_log_placements WHERE user_id = $1`, userID); n != 1 {
		t.Errorf("placements after commit = %d, want 1", n)
	}
}
