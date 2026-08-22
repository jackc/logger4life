package core

import (
	"fmt"

	"github.com/gofrs/uuid/v5"
)

// validID rejects an identifier that cannot name a record. Every ID a caller
// supplies was minted by the store as a UUID, so a string that is not one
// names nothing. Rejecting it here rather than passing it down keeps a bad
// path segment from reaching PostgreSQL as a cast error, which surfaced as a
// 500 and carried a database detail out past the store boundary.
func validID(field, id string) error {
	if _, err := uuid.FromString(id); err != nil {
		return fmt.Errorf("%s is invalid", field)
	}
	return nil
}

// validOptionalID applies validID where absence is meaningful — no parent
// folder, the root of the tree — and only a present value has to name a row.
func validOptionalID(field string, id *string) error {
	if id == nil {
		return nil
	}
	return validID(field, *id)
}
