package pgstore

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/logger4life/test/testutil"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/testdb"
)

var testDBManager *testdb.Manager

func TestMain(m *testing.M) {
	testDBManager = testutil.InitTestDBManager(m)
	os.Exit(m.Run())
}

func acquireTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	return testDBManager.AcquireDB(t, ctx).PoolConnect(t, ctx)
}
