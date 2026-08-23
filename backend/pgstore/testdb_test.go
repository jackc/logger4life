package pgstore

import "os"

// testDatabaseURL is where the test suites find logger4life_test.
//
// Each worktree runs its own PostgreSQL cluster on its own port, so the
// connection string is generated rather than fixed (see scripts/devports).
// The fallback is the conventional local cluster, which keeps a bare
// `go test` working outside the development environment.
func testDatabaseURL() string {
	if url := os.Getenv("TEST_DATABASE_URL"); url != "" {
		return url
	}
	return "postgres://postgres:postgres@localhost:5432/logger4life_test"
}
