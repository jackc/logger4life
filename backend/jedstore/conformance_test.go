package jedstore

import (
	"testing"

	"github.com/jackc/logger4life/backend/core/storetest"
)

// TestStoreConformance drives the embedded implementation through the same
// backend-independent contract as PostgreSQL.
func TestStoreConformance(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("closing jed store: %v", err)
		}
	})
	storetest.Run(t, store)
}
