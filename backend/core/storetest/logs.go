package storetest

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/logger4life/backend/core"
	"github.com/jackc/logger4life/backend/domain"
)

func doseField() domain.FieldDefinition {
	return domain.FieldDefinition{Name: "dose", Type: "number", Required: true}
}

// RunLogStore checks the port that owns the central record of the app. Its
// user scope is the one that matters most: a log is the unit of sharing, so a
// method that ignored the user would hand over somebody's whole history.
func RunLogStore(t *testing.T, ports Ports) {
	ctx := context.Background()

	t.Run("round trips a log and its field definitions", func(t *testing.T) {
		owner := newUser(t, ports)
		log := newLog(t, ports, owner.ID, "Vitamins", doseField())

		if log.Name != "Vitamins" || !log.IsOwner {
			t.Errorf("created %#v, want a log owned by the creator", log)
		}
		if len(log.Fields) != 1 || log.Fields[0].Name != "dose" {
			t.Errorf("Fields = %#v, want the definition it was created with", log.Fields)
		}

		read, err := ports.GetLog(ctx, owner.ID, log.ID)
		if err != nil {
			t.Fatal(err)
		}
		if read.ID != log.ID || read.Name != log.Name || len(read.Fields) != 1 {
			t.Errorf("GetLog = %#v, want the log that was written", read)
		}
		if !read.IsOwner || read.PinnedToHome != log.PinnedToHome {
			t.Errorf("GetLog lost the creator's placement: %#v", read)
		}
	})

	// A log with no custom fields must come back as an empty list, never as a
	// missing one: the value is serialized straight to a client that renders
	// [] and null differently.
	t.Run("returns an empty field list rather than none", func(t *testing.T) {
		owner := newUser(t, ports)
		log, err := ports.CreateLog(ctx, owner.ID, "Water", nil)
		if err != nil {
			t.Fatal(err)
		}
		if log.Fields == nil {
			t.Error("CreateLog returned nil fields for a log with none")
		}

		read, err := ports.GetLog(ctx, owner.ID, log.ID)
		if err != nil {
			t.Fatal(err)
		}
		if read.Fields == nil {
			t.Error("GetLog returned nil fields for a log with none")
		}

		updated, err := ports.UpdateLog(ctx, owner.ID, log.ID, "Water", nil)
		if err != nil {
			t.Fatal(err)
		}
		if updated.Fields == nil {
			t.Error("UpdateLog returned nil fields for a log with none")
		}
	})

	// The unique index covers (user, lower(name)), so the collision is per
	// owner and blind to case — two people may each keep a log called
	// Vitamins, and neither may keep two.
	t.Run("scopes the name collision to one owner and ignores case", func(t *testing.T) {
		owner := newUser(t, ports)
		newLog(t, ports, owner.ID, "Vitamins")

		if _, err := ports.CreateLog(ctx, owner.ID, "VITAMINS", nil); !errors.Is(err, core.ErrLogNameTaken) {
			t.Errorf("re-using the name = %v, want ErrLogNameTaken", err)
		}

		stranger := newUser(t, ports)
		if _, err := ports.CreateLog(ctx, stranger.ID, "Vitamins", nil); err != nil {
			t.Errorf("another user reusing the name = %v, want it allowed", err)
		}
	})

	t.Run("refuses to rename a log onto a name already in use", func(t *testing.T) {
		owner := newUser(t, ports)
		newLog(t, ports, owner.ID, "Vitamins")
		other := newLog(t, ports, owner.ID, "Pushups")

		if _, err := ports.UpdateLog(ctx, owner.ID, other.ID, "vitamins", nil); !errors.Is(err, core.ErrLogNameTaken) {
			t.Errorf("renaming onto an existing name = %v, want ErrLogNameTaken", err)
		}
	})

	// The load-bearing case. Every method takes a user, and every one of them
	// must treat another user's log exactly as it treats a log that does not
	// exist — no reads, no writes, and no hint that the ID was real.
	t.Run("hides another user's log from every method", func(t *testing.T) {
		owner := newUser(t, ports)
		stranger := newUser(t, ports)
		log := newLog(t, ports, owner.ID, "Private", doseField())

		if _, err := ports.GetLog(ctx, stranger.ID, log.ID); !errors.Is(err, core.ErrLogNotFound) {
			t.Errorf("GetLog as a stranger = %v, want ErrLogNotFound", err)
		}
		if _, err := ports.UpdateLog(ctx, stranger.ID, log.ID, "Hijacked", nil); !errors.Is(err, core.ErrLogNotFound) {
			t.Errorf("UpdateLog as a stranger = %v, want ErrLogNotFound", err)
		}
		if err := ports.DeleteLog(ctx, stranger.ID, log.ID); !errors.Is(err, core.ErrLogNotFound) {
			t.Errorf("DeleteLog as a stranger = %v, want ErrLogNotFound", err)
		}

		// A refused write must also be a write that did not happen.
		survivor, err := ports.GetLog(ctx, owner.ID, log.ID)
		if err != nil {
			t.Fatalf("the owner's log did not survive a stranger's writes: %v", err)
		}
		if survivor.Name != "Private" || len(survivor.Fields) != 1 {
			t.Errorf("the owner's log was modified by a stranger: %#v", survivor)
		}

		logs, err := ports.ListLogs(ctx, stranger.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, l := range logs {
			if l.ID == log.ID {
				t.Error("ListLogs returned another user's log")
			}
		}
	})

	t.Run("reports an unknown log rather than a zero one", func(t *testing.T) {
		owner := newUser(t, ports)
		if _, err := ports.GetLog(ctx, owner.ID, UnknownID); !errors.Is(err, core.ErrLogNotFound) {
			t.Errorf("GetLog error = %v, want ErrLogNotFound", err)
		}
		if _, err := ports.UpdateLog(ctx, owner.ID, UnknownID, "Name", nil); !errors.Is(err, core.ErrLogNotFound) {
			t.Errorf("UpdateLog error = %v, want ErrLogNotFound", err)
		}
		if err := ports.DeleteLog(ctx, owner.ID, UnknownID); !errors.Is(err, core.ErrLogNotFound) {
			t.Errorf("DeleteLog error = %v, want ErrLogNotFound", err)
		}
	})

	t.Run("deletes a log so it can no longer be read", func(t *testing.T) {
		owner := newUser(t, ports)
		log := newLog(t, ports, owner.ID, "Temporary")

		if err := ports.DeleteLog(ctx, owner.ID, log.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := ports.GetLog(ctx, owner.ID, log.ID); !errors.Is(err, core.ErrLogNotFound) {
			t.Errorf("GetLog after delete = %v, want ErrLogNotFound", err)
		}
		// The name is free again, which proves the row went rather than being
		// hidden behind a flag.
		if _, err := ports.CreateLog(ctx, owner.ID, "Temporary", nil); err != nil {
			t.Errorf("re-creating the deleted log = %v, want it allowed", err)
		}
	})

	// A user with no logs yields an empty list, not a nil one, for the same
	// reason an empty field list must not be nil.
	t.Run("lists a user's own logs and nothing else", func(t *testing.T) {
		owner := newUser(t, ports)
		empty, err := ports.ListLogs(ctx, owner.ID)
		if err != nil {
			t.Fatal(err)
		}
		if empty == nil {
			t.Error("ListLogs returned nil for a user with no logs")
		}
		if len(empty) != 0 {
			t.Errorf("a new user already has %d logs", len(empty))
		}

		names := []string{"Alpha", "Beta", "Gamma"}
		for _, name := range names {
			newLog(t, ports, owner.ID, name)
		}
		listed, err := ports.ListLogs(ctx, owner.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(listed) != len(names) {
			t.Fatalf("ListLogs returned %d logs, want %d", len(listed), len(names))
		}
		var got []string
		for _, l := range listed {
			if !l.IsOwner {
				t.Errorf("%q came back as not owned by its creator", l.Name)
			}
			got = append(got, l.Name)
		}
		if strings.Join(got, ",") != strings.Join(names, ",") {
			t.Errorf("ListLogs order = %v, want creation order %v", got, names)
		}
	})
}
