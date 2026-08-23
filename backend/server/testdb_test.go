package server

import (
	"context"
	"net/http/httptest"
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

type testServer struct {
	*httptest.Server
	db *testdb.DB
}

func (s *testServer) pgPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	return s.db.PoolConnect(t, context.Background())
}
