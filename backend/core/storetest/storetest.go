// Package storetest is a shared conformance suite for the driven ports in
// backend/core. Each RunX function drives one port through the contract its
// actions depend on, so any implementation can be checked the same way.
//
// The suite states three things a port must guarantee, in rising order of
// consequence. Values must survive a round trip, including the difference
// between an empty collection and a missing one, which JSON and JSONB both
// preserve and Go's zero values do not. Expected failures must arrive as the
// core sentinel for that condition rather than as whatever the underlying
// technology raised. And every method that takes a user must confine itself
// to that user's rows — the actions have no second line of defense, so a port
// that reads or writes across the boundary is a cross-account leak.
//
// Malformed identifiers are deliberately absent. Actions reject those before
// any store is reached (see TestActionsRejectMalformedIDs in backend/core), so
// what a port owes is defined for identifiers that are well formed but name
// nothing, or name something belonging to somebody else.
package storetest

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/jackc/logger4life/backend/core"
	"github.com/jackc/logger4life/backend/domain"
)

// Ports is the full set the suite drives. A store implements all of it to be
// checked by Run. The individual RunX functions take the whole set rather than
// the single port they exercise because their fixtures — a user to scope by, a
// log to hang entries from — necessarily come from neighboring ports.
type Ports interface {
	core.UserStore
	core.SessionStore
	core.LogStore
	core.LogEntryStore
	core.LogPlacementStore
	core.FolderStore
	core.SavedQueryStore
	core.SharingStore
}

// UserPrefix begins the username of every user the suite creates. A harness
// clears rows under this prefix before and after a run; the suite itself
// issues no SQL, so isolating it from a shared database is the harness's job.
const UserPrefix = "storetest_"

// UnknownID is well formed and names nothing. Ports must answer it with their
// not-found sentinel.
const UnknownID = "00000000-0000-4000-8000-0000000000ff"

var userSeq atomic.Uint64

// newUser creates a fixture user with a name unique to this process, so
// subtests that share a database cannot collide on the username index.
func newUser(t *testing.T, ports Ports) core.User {
	t.Helper()
	name := fmt.Sprintf("%s%d", UserPrefix, userSeq.Add(1))
	user, err := ports.CreateUser(context.Background(), name, nil, "fixture-hash")
	if err != nil {
		t.Fatalf("creating fixture user %s: %v", name, err)
	}
	return user
}

// newLog creates a fixture log owned by user.
func newLog(t *testing.T, ports Ports, userID, name string, fields ...domain.FieldDefinition) core.Log {
	t.Helper()
	log, err := ports.CreateLog(context.Background(), userID, name, fields)
	if err != nil {
		t.Fatalf("creating fixture log %q: %v", name, err)
	}
	return log
}

// Run drives every port in the suite.
func Run(t *testing.T, ports Ports) {
	t.Run("UserStore", func(t *testing.T) { RunUserStore(t, ports) })
	t.Run("SessionStore", func(t *testing.T) { RunSessionStore(t, ports) })
	t.Run("LogStore", func(t *testing.T) { RunLogStore(t, ports) })
	t.Run("LogEntryStore", func(t *testing.T) { RunLogEntryStore(t, ports) })
	t.Run("FolderStore", func(t *testing.T) { RunFolderStore(t, ports) })
	t.Run("LogPlacementStore", func(t *testing.T) { RunLogPlacementStore(t, ports) })
	t.Run("SavedQueryStore", func(t *testing.T) { RunSavedQueryStore(t, ports) })
	t.Run("SharingStore", func(t *testing.T) { RunSharingStore(t, ports) })
}
