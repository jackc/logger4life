package dualstore_test

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/logger4life/backend/core/storetest"
	"github.com/jackc/logger4life/backend/dualstore"
	"github.com/jackc/logger4life/backend/jedstore"
	"github.com/jackc/logger4life/backend/pgstore"
	"github.com/jackc/logger4life/test/testutil"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/testdb"
)

var testDBManager *testdb.Manager

func TestMain(m *testing.M) {
	testDBManager = testutil.InitTestDBManager(m)
	os.Exit(m.Run())
}

func TestStoreConformance(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := testDBManager.AcquireDB(t, ctx)
	pool, err := pgxpool.New(ctx, testutil.DatabaseURL(db))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	jedStore, err := jedstore.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = jedStore.Close() })

	storetest.Run(t, dualstore.New(pgstore.New(pool), jedStore))
}
