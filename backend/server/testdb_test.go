package server

import (
	"context"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/jackc/logger4life/backend/core"
	"github.com/jackc/logger4life/test/testutil"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/testdb"
)

var testDBManager *testdb.Manager

func TestMain(m *testing.M) {
	if usesPostgres() {
		testDBManager = testutil.InitTestDBManager(m)
	}
	os.Exit(m.Run())
}

// testBackend selects the adapter exercised by the server suite. The Rake
// task runs the package once with each supported value.
func testBackend() string {
	if backend := os.Getenv("LOGGER4LIFE_TEST_BACKEND"); backend != "" {
		return backend
	}
	return "postgresql"
}

func usesPostgres() bool { return testBackend() != "jed" }

type testServer struct {
	*httptest.Server
	db  *testdb.DB
	app *core.Core
}

func (s *testServer) pgPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if s.db == nil {
		t.Fatal("direct PostgreSQL inspection requires a postgres-backed test adapter")
	}
	return s.db.PoolConnect(t, context.Background())
}
