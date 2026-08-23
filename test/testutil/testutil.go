// Package testutil provides shared test-database plumbing: a testdb.Manager
// connected to the primary test database hands out exclusive copies that are
// reset with pgundolog between tests. The databases are prepared by
// `rake test:prepare`.
package testutil

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/testdb"
)

// InitTestDBManager performs the standard initialization of a testdb.Manager.
// Requiring testing.M keeps initialization in TestMain, before parallel tests
// begin acquiring databases.
func InitTestDBManager(*testing.M) *testdb.Manager {
	testDatabase := os.Getenv("TEST_DATABASE")
	if testDatabase == "" {
		fmt.Println("TEST_DATABASE is not set: run tests in the project environment after `rake test:prepare`")
		os.Exit(1)
	}

	manager := &testdb.Manager{
		ResetDB: func(ctx context.Context, conn *pgx.Conn) error {
			_, err := conn.Exec(ctx, `select pgundolog.undo()`)
			return err
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := manager.Connect(ctx, fmt.Sprintf("dbname=%s", testDatabase)); err != nil {
		fmt.Println("failed to initialize testdb.Manager:", err)
		os.Exit(1)
	}

	return manager
}
