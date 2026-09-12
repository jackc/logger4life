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

func TestOAuthFamilyRevocationConcurrency(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	storetest.RunOAuthFamilyRevocationConcurrency(t, store)
}

func TestOAuthClientLimits(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	storetest.RunOAuthClientLimits(t, store)
}
