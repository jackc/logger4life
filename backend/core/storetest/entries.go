package storetest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/logger4life/backend/core"
)

// RunLogEntryStore checks the port that records the events a log exists to
// hold.
//
// Its access rule differs from the other ports and is worth stating plainly:
// LogFieldDefinitions is the visibility check, and the write methods scope
// themselves to the log rather than to the user. Every action calls
// LogFieldDefinitions first, so the effective rule is the same — but an
// implementation that dropped the check from LogFieldDefinitions would open
// every log at once, which is why it is tested here directly.
func RunLogEntryStore(t *testing.T, ports Ports) {
	ctx := context.Background()
	occurred := time.Date(2026, 8, 22, 12, 0, 0, 0, time.UTC)

	t.Run("returns the definitions a log was created with", func(t *testing.T) {
		owner := newUser(t, ports)
		log := newLog(t, ports, owner.ID, "Vitamins", doseField())

		defs, err := ports.LogFieldDefinitions(ctx, owner.ID, log.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(defs) != 1 || defs[0].Name != "dose" || !defs[0].Required {
			t.Errorf("definitions = %#v, want the one the log was created with", defs)
		}
	})

	// The gate. Entry validation is performed against whatever this returns,
	// so a stranger who got definitions back would also get to write.
	t.Run("hides the definitions of another user's log", func(t *testing.T) {
		owner := newUser(t, ports)
		stranger := newUser(t, ports)
		log := newLog(t, ports, owner.ID, "Private", doseField())

		if _, err := ports.LogFieldDefinitions(ctx, stranger.ID, log.ID); !errors.Is(err, core.ErrLogNotFound) {
			t.Errorf("LogFieldDefinitions as a stranger = %v, want ErrLogNotFound", err)
		}
		if _, err := ports.ListLogEntries(ctx, stranger.ID, log.ID); !errors.Is(err, core.ErrLogNotFound) {
			t.Errorf("ListLogEntries as a stranger = %v, want ErrLogNotFound", err)
		}
		if err := ports.DeleteLogEntry(ctx, stranger.ID, log.ID, UnknownID); !errors.Is(err, core.ErrLogNotFound) {
			t.Errorf("DeleteLogEntry as a stranger = %v, want ErrLogNotFound", err)
		}
		if _, err := ports.LogFieldDefinitions(ctx, owner.ID, UnknownID); !errors.Is(err, core.ErrLogNotFound) {
			t.Errorf("LogFieldDefinitions for an unknown log = %v, want ErrLogNotFound", err)
		}
	})

	t.Run("round trips entry values and records their author", func(t *testing.T) {
		owner := newUser(t, ports)
		log := newLog(t, ports, owner.ID, "Vitamins", doseField())

		entry, err := ports.CreateLogEntry(ctx, owner.ID, log.ID, map[string]any{"dose": float64(500)})
		if err != nil {
			t.Fatal(err)
		}
		if entry.ID == "" || entry.LogID != log.ID {
			t.Errorf("created %#v, want an entry in %s", entry, log.ID)
		}
		if entry.UserID != owner.ID || entry.Username != owner.Username {
			t.Errorf("entry author = (%s, %s), want the writer", entry.UserID, entry.Username)
		}
		if entry.Fields["dose"] != float64(500) {
			t.Errorf("Fields = %#v, want the values written", entry.Fields)
		}
		if entry.OccurredAt.IsZero() {
			t.Error("OccurredAt was not defaulted; a new entry happened now")
		}
	})

	// An entry in a log with no fields must come back as an empty object
	// rather than a missing one, for the same reason a log's field list must.
	t.Run("returns empty entry values rather than none", func(t *testing.T) {
		owner := newUser(t, ports)
		log := newLog(t, ports, owner.ID, "Water")

		entry, err := ports.CreateLogEntry(ctx, owner.ID, log.ID, map[string]any{})
		if err != nil {
			t.Fatal(err)
		}
		if entry.Fields == nil {
			t.Error("CreateLogEntry returned nil fields for an entry with none")
		}
		listed, err := ports.ListLogEntries(ctx, owner.ID, log.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(listed) != 1 || listed[0].Fields == nil {
			t.Errorf("ListLogEntries returned %#v, want one entry with empty fields", listed)
		}
	})

	// Newest first is what the log detail page renders, and it is the store's
	// job rather than the caller's.
	t.Run("lists entries newest first", func(t *testing.T) {
		owner := newUser(t, ports)
		log := newLog(t, ports, owner.ID, "Vitamins")

		empty, err := ports.ListLogEntries(ctx, owner.ID, log.ID)
		if err != nil {
			t.Fatal(err)
		}
		if empty == nil {
			t.Error("ListLogEntries returned nil for a log with no entries")
		}

		var ids []string
		for range 3 {
			entry, err := ports.CreateLogEntry(ctx, owner.ID, log.ID, map[string]any{})
			if err != nil {
				t.Fatal(err)
			}
			ids = append(ids, entry.ID)
		}
		// Give them distinct times in a known order.
		for i, id := range ids {
			if _, err := ports.UpdateLogEntry(ctx, owner.ID, log.ID, id, map[string]any{}, occurred.Add(time.Duration(i)*time.Hour)); err != nil {
				t.Fatal(err)
			}
		}

		listed, err := ports.ListLogEntries(ctx, owner.ID, log.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(listed) != 3 {
			t.Fatalf("ListLogEntries returned %d entries, want 3", len(listed))
		}
		for i := 1; i < len(listed); i++ {
			if listed[i-1].OccurredAt.Before(listed[i].OccurredAt) {
				t.Fatalf("entry %d occurred before entry %d; the list is not newest first", i-1, i)
			}
		}
		if listed[0].ID != ids[2] {
			t.Errorf("first entry = %s, want the most recent %s", listed[0].ID, ids[2])
		}
	})

	t.Run("updates an entry in place", func(t *testing.T) {
		owner := newUser(t, ports)
		log := newLog(t, ports, owner.ID, "Vitamins", doseField())
		entry, err := ports.CreateLogEntry(ctx, owner.ID, log.ID, map[string]any{"dose": float64(500)})
		if err != nil {
			t.Fatal(err)
		}

		updated, err := ports.UpdateLogEntry(ctx, owner.ID, log.ID, entry.ID, map[string]any{"dose": float64(250)}, occurred)
		if err != nil {
			t.Fatal(err)
		}
		if updated.ID != entry.ID {
			t.Errorf("UpdateLogEntry returned %s, want the entry it updated", updated.ID)
		}
		if updated.Fields["dose"] != float64(250) || !updated.OccurredAt.Equal(occurred) {
			t.Errorf("updated = %#v, want the new values", updated)
		}
		if updated.UserID != entry.UserID {
			t.Error("updating an entry reassigned its author")
		}
	})

	// The write methods match on the log as well as the entry, so an entry ID
	// borrowed from a log the caller cannot see still finds nothing.
	t.Run("will not reach an entry through the wrong log", func(t *testing.T) {
		owner := newUser(t, ports)
		visible := newLog(t, ports, owner.ID, "Visible")
		other := newLog(t, ports, owner.ID, "Other")
		entry, err := ports.CreateLogEntry(ctx, owner.ID, other.ID, map[string]any{})
		if err != nil {
			t.Fatal(err)
		}

		if _, err := ports.UpdateLogEntry(ctx, owner.ID, visible.ID, entry.ID, map[string]any{}, occurred); !errors.Is(err, core.ErrLogEntryNotFound) {
			t.Errorf("updating through the wrong log = %v, want ErrLogEntryNotFound", err)
		}
		if err := ports.DeleteLogEntry(ctx, owner.ID, visible.ID, entry.ID); !errors.Is(err, core.ErrLogEntryNotFound) {
			t.Errorf("deleting through the wrong log = %v, want ErrLogEntryNotFound", err)
		}

		survivors, err := ports.ListLogEntries(ctx, owner.ID, other.ID)
		if err != nil || len(survivors) != 1 {
			t.Errorf("the entry did not survive: %#v, %v", survivors, err)
		}
	})

	t.Run("reports an unknown entry rather than a zero one", func(t *testing.T) {
		owner := newUser(t, ports)
		log := newLog(t, ports, owner.ID, "Vitamins")

		if _, err := ports.UpdateLogEntry(ctx, owner.ID, log.ID, UnknownID, map[string]any{}, occurred); !errors.Is(err, core.ErrLogEntryNotFound) {
			t.Errorf("UpdateLogEntry error = %v, want ErrLogEntryNotFound", err)
		}
		if err := ports.DeleteLogEntry(ctx, owner.ID, log.ID, UnknownID); !errors.Is(err, core.ErrLogEntryNotFound) {
			t.Errorf("DeleteLogEntry error = %v, want ErrLogEntryNotFound", err)
		}
	})

	t.Run("deletes an entry", func(t *testing.T) {
		owner := newUser(t, ports)
		log := newLog(t, ports, owner.ID, "Vitamins")
		entry, err := ports.CreateLogEntry(ctx, owner.ID, log.ID, map[string]any{})
		if err != nil {
			t.Fatal(err)
		}

		if err := ports.DeleteLogEntry(ctx, owner.ID, log.ID, entry.ID); err != nil {
			t.Fatal(err)
		}
		remaining, err := ports.ListLogEntries(ctx, owner.ID, log.ID)
		if err != nil || len(remaining) != 0 {
			t.Errorf("after delete the log holds %#v, %v", remaining, err)
		}
	})

	// Deleting a log takes its entries with it; nothing else has to clean up.
	t.Run("discards entries when their log is deleted", func(t *testing.T) {
		owner := newUser(t, ports)
		log := newLog(t, ports, owner.ID, "Ephemeral")
		if _, err := ports.CreateLogEntry(ctx, owner.ID, log.ID, map[string]any{}); err != nil {
			t.Fatal(err)
		}
		if err := ports.DeleteLog(ctx, owner.ID, log.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := ports.ListLogEntries(ctx, owner.ID, log.ID); !errors.Is(err, core.ErrLogNotFound) {
			t.Errorf("listing entries of a deleted log = %v, want ErrLogNotFound", err)
		}
	})

}
