package core

import (
	"crypto/sha256"
	"testing"

	"github.com/gofrs/uuid/v5"
)

// testID turns a readable label into a stable, well-formed identifier.
// Actions reject a malformed ID before any store is reached, so a placeholder
// like "log-1" never gets far enough to exercise the behavior under test —
// while a bare UUID literal in every assertion would be unreadable.
func testID(label string) string {
	sum := sha256.Sum256([]byte(label))
	id, err := uuid.FromBytes(sum[:16])
	if err != nil {
		panic(err)
	}
	return id.String()
}

func TestValidIDAcceptsOnlyWellFormedIdentifiers(t *testing.T) {
	accepted := []string{
		testID("log-1"),
		"00000000-0000-0000-0000-000000000000",
		"0190A1B2-C3D4-7000-8000-000000000001", // upper case is still a UUID
	}
	for _, id := range accepted {
		if err := validID("log_id", id); err != nil {
			t.Errorf("validID(%q) = %v, want nil", id, err)
		}
	}

	rejected := []string{
		"",
		" ",
		"not-a-uuid",
		"0190a1b2-c3d4-7000-8000",              // truncated
		"0190a1b2-c3d4-7000-8000-00000000000g", // not hex
		"0190a1b2-c3d4-7000-8000-000000000001 OR 1=1", // trailing junk
	}
	for _, id := range rejected {
		err := validID("log_id", id)
		if err == nil {
			t.Errorf("validID(%q) = nil, want an error", id)
			continue
		}
		// The message names the field so a caller can tell which of several
		// IDs in one request was wrong.
		if err.Error() != "log_id is invalid" {
			t.Errorf("validID(%q) = %q, want it to name log_id", id, err)
		}
	}
}

// A nil optional ID means "no parent" or "the root", which is a value in its
// own right and not something to reject.
func TestValidOptionalIDAllowsAbsence(t *testing.T) {
	if err := validOptionalID("parent_folder_id", nil); err != nil {
		t.Errorf("validOptionalID(nil) = %v, want nil", err)
	}
	good := testID("folder-1")
	if err := validOptionalID("parent_folder_id", &good); err != nil {
		t.Errorf("validOptionalID(%q) = %v, want nil", good, err)
	}
	bad := "not-a-uuid"
	if err := validOptionalID("parent_folder_id", &bad); err == nil {
		t.Error("validOptionalID accepted a malformed ID")
	}
}
