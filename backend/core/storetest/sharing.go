package storetest

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/logger4life/backend/core"
)

// RunSharingStore checks the port that lets one user hand another access to a
// log. It carries the app's only deliberate cross-user path, so the boundary
// it draws is what stops that path from becoming a general one: a share token
// admits its holder to exactly one log, as a member and not as an owner.
func RunSharingStore(t *testing.T, ports Ports) {
	ctx := context.Background()

	// shared sets up an owner with a tokenized log and a second user who has
	// not joined it yet.
	shared := func(t *testing.T) (owner core.User, joiner core.User, log core.Log, token []byte) {
		t.Helper()
		owner = newUser(t, ports)
		joiner = newUser(t, ports)
		log = newLog(t, ports, owner.ID, "Shared", doseField())
		token = []byte("token-" + owner.Username)
		if err := ports.CreateShareToken(ctx, owner.ID, log.ID, token); err != nil {
			t.Fatalf("creating the share token: %v", err)
		}
		return owner, joiner, log, token
	}

	t.Run("issues and revokes a share token", func(t *testing.T) {
		owner, _, log, token := shared(t)

		info, err := ports.GetShareInfo(ctx, owner.ID, token)
		if err != nil {
			t.Fatal(err)
		}
		if info.LogID != log.ID || info.LogName != "Shared" || info.OwnerUsername != owner.Username {
			t.Errorf("share info = %#v, want it to describe the shared log", info)
		}

		if err := ports.DeleteShareToken(ctx, owner.ID, log.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := ports.GetShareInfo(ctx, owner.ID, token); !errors.Is(err, core.ErrInvalidShareLink) {
			t.Errorf("a revoked token still resolves: %v", err)
		}
	})

	// Only the owner controls sharing. A member who joined has full use of the
	// entries but must not be able to re-share the log or read its roster.
	t.Run("keeps share management with the owner", func(t *testing.T) {
		owner, joiner, log, token := shared(t)
		if _, err := ports.JoinSharedLog(ctx, joiner.ID, token); err != nil {
			t.Fatal(err)
		}

		if err := ports.CreateShareToken(ctx, joiner.ID, log.ID, []byte("member-token")); !errors.Is(err, core.ErrLogNotFound) {
			t.Errorf("a member issuing a token = %v, want ErrLogNotFound", err)
		}
		if err := ports.DeleteShareToken(ctx, joiner.ID, log.ID); !errors.Is(err, core.ErrLogNotFound) {
			t.Errorf("a member revoking the token = %v, want ErrLogNotFound", err)
		}
		if _, err := ports.ListSharedUsers(ctx, joiner.ID, log.ID); !errors.Is(err, core.ErrLogNotFound) {
			t.Errorf("a member reading the roster = %v, want ErrLogNotFound", err)
		}
		if err := ports.RemoveSharedUser(ctx, joiner.ID, log.ID, UnknownID); !errors.Is(err, core.ErrLogNotFound) {
			t.Errorf("a member removing a share = %v, want ErrLogNotFound", err)
		}

		// The owner can still do all of it, so the refusals above are about
		// who asked rather than about the log being unreachable.
		roster, err := ports.ListSharedUsers(ctx, owner.ID, log.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(roster) != 1 || roster[0].Username != joiner.Username {
			t.Errorf("roster = %#v, want the one member", roster)
		}
	})

	t.Run("refuses an unknown or unrelated token", func(t *testing.T) {
		owner := newUser(t, ports)
		if _, err := ports.GetShareInfo(ctx, owner.ID, []byte("no-such-token")); !errors.Is(err, core.ErrInvalidShareLink) {
			t.Errorf("GetShareInfo error = %v, want ErrInvalidShareLink", err)
		}
		if _, err := ports.JoinSharedLog(ctx, owner.ID, []byte("no-such-token")); !errors.Is(err, core.ErrInvalidShareLink) {
			t.Errorf("JoinSharedLog error = %v, want ErrInvalidShareLink", err)
		}
		if err := ports.CreateShareToken(ctx, owner.ID, UnknownID, []byte("t")); !errors.Is(err, core.ErrLogNotFound) {
			t.Errorf("CreateShareToken for an unknown log = %v, want ErrLogNotFound", err)
		}
	})

	// Joining is what turns a token into access, so the effect is checked
	// through a neighboring port: the log the joiner could not read before
	// becomes readable, and not as its owner.
	t.Run("admits a joiner as a member and not as an owner", func(t *testing.T) {
		owner, joiner, log, token := shared(t)

		if _, err := ports.GetLog(ctx, joiner.ID, log.ID); !errors.Is(err, core.ErrLogNotFound) {
			t.Fatalf("the joiner could already read the log before joining: %v", err)
		}

		result, err := ports.JoinSharedLog(ctx, joiner.ID, token)
		if err != nil {
			t.Fatal(err)
		}
		if result.LogID != log.ID || result.AlreadyMember {
			t.Errorf("join result = %#v, want a first-time join of the shared log", result)
		}

		joined, err := ports.GetLog(ctx, joiner.ID, log.ID)
		if err != nil {
			t.Fatalf("the joiner still cannot read the log: %v", err)
		}
		if joined.IsOwner {
			t.Error("the joiner reads the log as its owner")
		}
		if joined.ShareToken != nil {
			t.Error("the joiner can read the share token, which is the owner's to hold")
		}

		// The joiner gets their own placement, so the log appears on their
		// home screen rather than only in the owner's tree.
		listed, err := ports.ListLogs(ctx, joiner.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(listed) != 1 || listed[0].ID != log.ID {
			t.Errorf("the joiner's logs = %#v, want the shared log", listed)
		}

		// A member may write entries; that is the point of sharing.
		if _, err := ports.CreateLogEntry(ctx, joiner.ID, log.ID, map[string]any{"dose": float64(1)}); err != nil {
			t.Errorf("a member writing an entry = %v, want it allowed", err)
		}
		_ = owner
	})

	// Following the same link twice is normal — a bookmark, a refresh — and
	// must be reported rather than duplicating the membership.
	t.Run("reports a repeat join instead of duplicating it", func(t *testing.T) {
		owner, joiner, log, token := shared(t)
		if _, err := ports.JoinSharedLog(ctx, joiner.ID, token); err != nil {
			t.Fatal(err)
		}

		again, err := ports.JoinSharedLog(ctx, joiner.ID, token)
		if err != nil {
			t.Fatal(err)
		}
		if !again.AlreadyMember {
			t.Error("joining twice did not report an existing membership")
		}

		roster, err := ports.ListSharedUsers(ctx, owner.ID, log.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(roster) != 1 {
			t.Errorf("roster holds %d entries after joining twice, want 1", len(roster))
		}

		info, err := ports.GetShareInfo(ctx, joiner.ID, token)
		if err != nil {
			t.Fatal(err)
		}
		if !info.AlreadyMember || info.IsOwner {
			t.Errorf("share info for a member = %#v, want AlreadyMember without IsOwner", info)
		}
	})

	// An owner following their own link is a mistake worth naming, not a
	// silent no-op: they would otherwise appear on their own roster.
	t.Run("refuses to let an owner join their own log", func(t *testing.T) {
		owner, _, _, token := shared(t)

		info, err := ports.GetShareInfo(ctx, owner.ID, token)
		if err != nil {
			t.Fatal(err)
		}
		if !info.IsOwner || info.AlreadyMember {
			t.Errorf("share info for the owner = %#v, want IsOwner without AlreadyMember", info)
		}
		if _, err := ports.JoinSharedLog(ctx, owner.ID, token); !errors.Is(err, core.ErrAlreadyOwnLog) {
			t.Errorf("the owner joining = %v, want ErrAlreadyOwnLog", err)
		}
	})

	// Removing a member has to revoke access, not merely clear a row from the
	// roster. The member keeps a placement — that is what lets a rejoin
	// restore the folder they had filed the log under — so every read has to
	// establish membership for itself rather than infer it from a placement.
	t.Run("removes a member and with them their access", func(t *testing.T) {
		owner, joiner, log, token := shared(t)
		if _, err := ports.JoinSharedLog(ctx, joiner.ID, token); err != nil {
			t.Fatal(err)
		}
		entry, err := ports.CreateLogEntry(ctx, joiner.ID, log.ID, map[string]any{"dose": float64(1)})
		if err != nil {
			t.Fatal(err)
		}
		roster, err := ports.ListSharedUsers(ctx, owner.ID, log.ID)
		if err != nil || len(roster) != 1 {
			t.Fatalf("roster = %#v, %v", roster, err)
		}

		if err := ports.RemoveSharedUser(ctx, owner.ID, log.ID, UnknownID); !errors.Is(err, core.ErrShareNotFound) {
			t.Errorf("removing an unknown share = %v, want ErrShareNotFound", err)
		}
		if err := ports.RemoveSharedUser(ctx, owner.ID, log.ID, roster[0].ID); err != nil {
			t.Fatal(err)
		}

		if _, err := ports.GetLog(ctx, joiner.ID, log.ID); !errors.Is(err, core.ErrLogNotFound) {
			t.Errorf("a removed member can still read the log: %v", err)
		}
		if _, err := ports.LogFieldDefinitions(ctx, joiner.ID, log.ID); !errors.Is(err, core.ErrLogNotFound) {
			t.Errorf("a removed member can still read the log's schema: %v", err)
		}
		if _, err := ports.ListLogEntries(ctx, joiner.ID, log.ID); !errors.Is(err, core.ErrLogNotFound) {
			t.Errorf("a removed member can still read the log's entries: %v", err)
		}
		if err := ports.DeleteLogEntry(ctx, joiner.ID, log.ID, entry.ID); !errors.Is(err, core.ErrLogNotFound) {
			t.Errorf("a removed member can still delete the log's entries: %v", err)
		}
		listed, err := ports.ListLogs(ctx, joiner.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, l := range listed {
			if l.ID == log.ID {
				t.Error("a removed member still lists the log")
			}
		}

		// The entry they wrote stays with the log; removing someone does not
		// erase the history they contributed.
		remaining, err := ports.ListLogEntries(ctx, owner.ID, log.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(remaining) != 1 || remaining[0].ID != entry.ID {
			t.Errorf("the owner's entries after a removal = %#v, want the member's entry kept", remaining)
		}
	})

	// The placement survives a removal on purpose, so that following the link
	// again puts the log back where the member had filed it.
	t.Run("restores a rejoining member to where they had filed the log", func(t *testing.T) {
		owner, joiner, log, token := shared(t)
		if _, err := ports.JoinSharedLog(ctx, joiner.ID, token); err != nil {
			t.Fatal(err)
		}
		folder, err := ports.CreateFolder(ctx, joiner.ID, "Theirs", nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := ports.UpdateLogPlacement(ctx, joiner.ID, log.ID, domainPlacement(folder.ID, 0)); err != nil {
			t.Fatal(err)
		}

		roster, err := ports.ListSharedUsers(ctx, owner.ID, log.ID)
		if err != nil || len(roster) != 1 {
			t.Fatalf("roster = %#v, %v", roster, err)
		}
		if err := ports.RemoveSharedUser(ctx, owner.ID, log.ID, roster[0].ID); err != nil {
			t.Fatal(err)
		}
		if _, err := ports.JoinSharedLog(ctx, joiner.ID, token); err != nil {
			t.Fatalf("rejoining = %v, want it allowed", err)
		}

		rejoined, err := ports.GetLog(ctx, joiner.ID, log.ID)
		if err != nil {
			t.Fatal(err)
		}
		if rejoined.FolderID == nil || *rejoined.FolderID != folder.ID {
			t.Errorf("after rejoining the log sits at %v, want the folder it was filed in", rejoined.FolderID)
		}
	})
}
