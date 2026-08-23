package pgstore

import (
	"testing"

	"github.com/jackc/logger4life/backend/core/storetest"
)

// TestStoreConformance drives the PostgreSQL store through the shared suite in
// backend/core/storetest, which states what every implementation of the ports
// owes its callers.
//
// The harness acquires an exclusive, pristine database copy for this test.
// That isolation is also what lets the pgstore and server packages run at the
// same time without either package's fixture lifecycle affecting the other.
func TestStoreConformance(t *testing.T) {
	t.Parallel()
	pool := acquireTestPool(t)
	storetest.Run(t, New(pool))
}
