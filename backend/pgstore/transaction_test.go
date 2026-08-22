package pgstore

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/logger4life/backend/domain"
	"github.com/jackc/pgx/v5/pgxpool"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), "postgres://postgres:postgres@localhost:5432/logger4life_test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		pool.Exec(ctx, "DELETE FROM user_log_placements")
		pool.Exec(ctx, "DELETE FROM folders")
		pool.Exec(ctx, "DELETE FROM logs")
		pool.Exec(ctx, "DELETE FROM users")
		pool.Close()
	})
	return New(pool)
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
	store := newTestStore(t)
	ctx := context.Background()
	fields := []domain.FieldDefinition{{Name: "Dose", Type: "number"}}

	rollback := errors.New("rolled back on purpose")
	err := store.InTx(ctx, func(ctx context.Context) error {
		user, err := store.CreateUser(ctx, "tx_user", nil, "hash")
		if err != nil {
			return err
		}
		if _, err := store.CreateLog(ctx, user.ID, "Vitamins", fields); err != nil {
			return err
		}
		if _, err := store.CreateFolder(ctx, user.ID, "Health", nil); err != nil {
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
	}{
		{"users", `SELECT count(*) FROM users WHERE username = 'tx_user'`},
		{"logs", `SELECT count(*) FROM logs WHERE name = 'Vitamins'`},
		{"folders", `SELECT count(*) FROM folders WHERE name = 'Health'`},
		{"placements", `SELECT count(*) FROM user_log_placements`},
	} {
		if n := countRows(t, store, c.sql); n != 0 {
			t.Errorf("%s rows after rollback = %d, want 0", c.what, n)
		}
	}

	// The same unit committing must persist every write.
	var userID string
	if err := store.InTx(ctx, func(ctx context.Context) error {
		user, err := store.CreateUser(ctx, "tx_user", nil, "hash")
		if err != nil {
			return err
		}
		userID = user.ID
		if _, err := store.CreateLog(ctx, user.ID, "Vitamins", fields); err != nil {
			return err
		}
		_, err = store.CreateFolder(ctx, user.ID, "Health", nil)
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
