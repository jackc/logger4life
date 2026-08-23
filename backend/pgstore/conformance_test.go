package pgstore

import (
	"context"
	"testing"

	"github.com/jackc/logger4life/backend/core/storetest"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TestStoreConformance drives the PostgreSQL store through the shared suite in
// backend/core/storetest, which states what every implementation of the ports
// owes its callers.
//
// Isolating the suite from a shared database is this harness's job, not the
// suite's: logger4life_test is also used by the server package and by the
// Playwright run, which leaves its rows behind. Everything the suite writes
// hangs off a user named with storetest.Prefix, so clearing those users
// clears the run by ON DELETE CASCADE — except OAuth clients, which belong to
// no user and are named with the same prefix instead.
func TestStoreConformance(t *testing.T) {
	pool, err := pgxpool.New(context.Background(), testDatabaseURL())
	if err != nil {
		t.Fatal(err)
	}
	clear := func() {
		for _, statement := range []string{
			`DELETE FROM users WHERE username LIKE $1`,
			`DELETE FROM oauth_clients WHERE id LIKE $1`,
		} {
			if _, err := pool.Exec(context.Background(), statement, storetest.Prefix+"%"); err != nil {
				t.Errorf("clearing suite rows: %v", err)
			}
		}
	}
	clear()
	t.Cleanup(func() {
		clear()
		pool.Close()
	})

	storetest.Run(t, New(pool))
}
