package core

import (
	"fmt"
	"uuid"
)

// validID rejects an identifier that cannot name a record. Every ID a caller
// supplies was minted by the application as a UUID, so a string that is not
// one names nothing. Rejecting it here keeps a bad path segment from reaching
// a persistence adapter as a database-specific cast error.
func validID(field, id string) error {
	if _, err := uuid.Parse(id); err != nil {
		return fmt.Errorf("%s is invalid", field)
	}
	return nil
}

func validOptionalID(field string, id *string) error {
	if id == nil {
		return nil
	}
	return validID(field, *id)
}

func newUserID() (string, error) {
	return uuid.NewV4().String(), nil
}

func newID() (string, error) {
	return uuid.NewV7().String(), nil
}
