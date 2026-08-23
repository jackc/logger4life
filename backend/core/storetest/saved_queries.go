package storetest

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/logger4life/backend/core"
)

// RunSavedQueryStore checks the port behind the saved SQL a user builds up.
func RunSavedQueryStore(t *testing.T, ports Ports) {
	ctx := context.Background()

	t.Run("round trips a saved query", func(t *testing.T) {
		owner := newUser(t, ports)
		created, err := ports.CreateSavedQuery(ctx, newRowID(), owner.ID, "Weekly", "SELECT 1")
		if err != nil {
			t.Fatal(err)
		}
		if created.ID == "" || created.Name != "Weekly" || created.QueryText != "SELECT 1" {
			t.Errorf("created %#v, want the query as written", created)
		}

		found, err := ports.GetSavedQueryByName(ctx, owner.ID, "Weekly")
		if err != nil {
			t.Fatal(err)
		}
		if found.ID != created.ID || found.QueryText != "SELECT 1" {
			t.Errorf("GetSavedQueryByName = %#v, want the query that was written", found)
		}
	})

	// Unlike a log name, which collides case-insensitively, a saved query name
	// is unique per user exactly as typed. The asymmetry is deliberate enough
	// to pin: a user may keep both "weekly" and "Weekly".
	t.Run("scopes the name collision to one user and respects case", func(t *testing.T) {
		owner := newUser(t, ports)
		if _, err := ports.CreateSavedQuery(ctx, newRowID(), owner.ID, "Weekly", "SELECT 1"); err != nil {
			t.Fatal(err)
		}
		if _, err := ports.CreateSavedQuery(ctx, newRowID(), owner.ID, "Weekly", "SELECT 2"); !errors.Is(err, core.ErrSavedQueryNameTaken) {
			t.Errorf("re-using the name = %v, want ErrSavedQueryNameTaken", err)
		}
		if _, err := ports.CreateSavedQuery(ctx, newRowID(), owner.ID, "weekly", "SELECT 2"); err != nil {
			t.Errorf("using the name in another case = %v, want it allowed", err)
		}

		stranger := newUser(t, ports)
		if _, err := ports.CreateSavedQuery(ctx, newRowID(), stranger.ID, "Weekly", "SELECT 3"); err != nil {
			t.Errorf("another user reusing the name = %v, want it allowed", err)
		}
	})

	t.Run("refuses to rename a query onto a name already in use", func(t *testing.T) {
		owner := newUser(t, ports)
		if _, err := ports.CreateSavedQuery(ctx, newRowID(), owner.ID, "Weekly", "SELECT 1"); err != nil {
			t.Fatal(err)
		}
		other, err := ports.CreateSavedQuery(ctx, newRowID(), owner.ID, "Monthly", "SELECT 2")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ports.UpdateSavedQuery(ctx, owner.ID, other.ID, "Weekly", "SELECT 2"); !errors.Is(err, core.ErrSavedQueryNameTaken) {
			t.Errorf("renaming onto an existing name = %v, want ErrSavedQueryNameTaken", err)
		}
	})

	t.Run("hides another user's saved query from every method", func(t *testing.T) {
		owner := newUser(t, ports)
		stranger := newUser(t, ports)
		query, err := ports.CreateSavedQuery(ctx, newRowID(), owner.ID, "Private", "SELECT 1")
		if err != nil {
			t.Fatal(err)
		}

		if _, err := ports.GetSavedQueryByName(ctx, stranger.ID, "Private"); !errors.Is(err, core.ErrSavedQueryNotFound) {
			t.Errorf("GetSavedQueryByName as a stranger = %v, want ErrSavedQueryNotFound", err)
		}
		if _, err := ports.UpdateSavedQuery(ctx, stranger.ID, query.ID, "Hijacked", "SELECT 2"); !errors.Is(err, core.ErrSavedQueryNotFound) {
			t.Errorf("UpdateSavedQuery as a stranger = %v, want ErrSavedQueryNotFound", err)
		}
		if err := ports.DeleteSavedQuery(ctx, stranger.ID, query.ID); !errors.Is(err, core.ErrSavedQueryNotFound) {
			t.Errorf("DeleteSavedQuery as a stranger = %v, want ErrSavedQueryNotFound", err)
		}

		survivor, err := ports.GetSavedQueryByName(ctx, owner.ID, "Private")
		if err != nil {
			t.Fatalf("the owner's query did not survive a stranger's writes: %v", err)
		}
		if survivor.QueryText != "SELECT 1" {
			t.Errorf("the owner's query was modified by a stranger: %#v", survivor)
		}

		listed, err := ports.ListSavedQueries(ctx, stranger.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(listed) != 0 {
			t.Errorf("ListSavedQueries returned %d queries for a user with none", len(listed))
		}
	})

	t.Run("reports an unknown query rather than a zero one", func(t *testing.T) {
		owner := newUser(t, ports)
		if _, err := ports.GetSavedQueryByName(ctx, owner.ID, "Absent"); !errors.Is(err, core.ErrSavedQueryNotFound) {
			t.Errorf("GetSavedQueryByName error = %v, want ErrSavedQueryNotFound", err)
		}
		if _, err := ports.UpdateSavedQuery(ctx, owner.ID, UnknownID, "Name", "SELECT 1"); !errors.Is(err, core.ErrSavedQueryNotFound) {
			t.Errorf("UpdateSavedQuery error = %v, want ErrSavedQueryNotFound", err)
		}
		if err := ports.DeleteSavedQuery(ctx, owner.ID, UnknownID); !errors.Is(err, core.ErrSavedQueryNotFound) {
			t.Errorf("DeleteSavedQuery error = %v, want ErrSavedQueryNotFound", err)
		}
	})

	t.Run("updates and deletes a query", func(t *testing.T) {
		owner := newUser(t, ports)
		query, err := ports.CreateSavedQuery(ctx, newRowID(), owner.ID, "Weekly", "SELECT 1")
		if err != nil {
			t.Fatal(err)
		}

		updated, err := ports.UpdateSavedQuery(ctx, owner.ID, query.ID, "Weekly totals", "SELECT 2")
		if err != nil {
			t.Fatal(err)
		}
		if updated.ID != query.ID || updated.Name != "Weekly totals" || updated.QueryText != "SELECT 2" {
			t.Errorf("updated = %#v, want the same query under the new values", updated)
		}
		if _, err := ports.GetSavedQueryByName(ctx, owner.ID, "Weekly"); !errors.Is(err, core.ErrSavedQueryNotFound) {
			t.Error("the old name still resolves after a rename")
		}

		if err := ports.DeleteSavedQuery(ctx, owner.ID, query.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := ports.GetSavedQueryByName(ctx, owner.ID, "Weekly totals"); !errors.Is(err, core.ErrSavedQueryNotFound) {
			t.Error("the query still resolves after being deleted")
		}
	})

	// The list is what the query page renders, so its order is the store's
	// responsibility: by name, blind to case, so "apple" and "Banana" sort as
	// a reader expects rather than by byte value.
	t.Run("lists a user's queries by name ignoring case", func(t *testing.T) {
		owner := newUser(t, ports)
		empty, err := ports.ListSavedQueries(ctx, owner.ID)
		if err != nil {
			t.Fatal(err)
		}
		if empty == nil {
			t.Error("ListSavedQueries returned nil for a user with none")
		}

		for _, name := range []string{"banana", "Apple", "cherry"} {
			if _, err := ports.CreateSavedQuery(ctx, newRowID(), owner.ID, name, "SELECT 1"); err != nil {
				t.Fatal(err)
			}
		}
		listed, err := ports.ListSavedQueries(ctx, owner.ID)
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, q := range listed {
			names = append(names, q.Name)
		}
		if want := "Apple,banana,cherry"; strings.Join(names, ",") != want {
			t.Errorf("ListSavedQueries order = %v, want %s", names, want)
		}
	})
}
