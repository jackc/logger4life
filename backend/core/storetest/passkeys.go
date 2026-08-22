package storetest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/logger4life/backend/core"
)

// RunPasskeyStore checks the port holding long-lived WebAuthn credentials.
func RunPasskeyStore(t *testing.T, ports Ports) {
	ctx := context.Background()

	newPasskey := func(t *testing.T, userID, suffix string) core.Passkey {
		t.Helper()
		created, err := ports.CreatePasskey(ctx, core.Passkey{
			UserID:         userID,
			CredentialID:   []byte("credential-" + suffix),
			PublicKey:      []byte("public-key-" + suffix),
			AAGUID:         []byte("aaguid"),
			SignCount:      1,
			BackupEligible: true,
			Description:    "Phone",
		})
		if err != nil {
			t.Fatalf("creating fixture passkey: %v", err)
		}
		return created
	}

	t.Run("round trips a credential", func(t *testing.T) {
		owner := newUser(t, ports)
		passkey := newPasskey(t, owner.ID, owner.Username)

		if passkey.ID == "" || passkey.UserID != owner.ID {
			t.Errorf("created %#v, want a passkey belonging to its user", passkey)
		}
		if string(passkey.CredentialID) != "credential-"+owner.Username {
			t.Errorf("CredentialID = %q, want the bytes written", passkey.CredentialID)
		}
		if string(passkey.PublicKey) != "public-key-"+owner.Username {
			t.Errorf("PublicKey = %q, want the bytes written", passkey.PublicKey)
		}
		if passkey.SignCount != 1 || !passkey.BackupEligible || passkey.Description != "Phone" {
			t.Errorf("created %#v, want the values written", passkey)
		}
	})

	// The credential ID is unique across the whole installation, not per
	// account: an authenticator already enrolled cannot be enrolled again,
	// whoever is asking.
	t.Run("refuses a credential that is already registered anywhere", func(t *testing.T) {
		owner := newUser(t, ports)
		newPasskey(t, owner.ID, owner.Username)

		if _, err := ports.CreatePasskey(ctx, core.Passkey{
			UserID: owner.ID, CredentialID: []byte("credential-" + owner.Username), PublicKey: []byte("k"), AAGUID: []byte("aaguid"),
		}); !errors.Is(err, core.ErrPasskeyAlreadyRegistered) {
			t.Errorf("re-registering to the same account = %v, want ErrPasskeyAlreadyRegistered", err)
		}

		stranger := newUser(t, ports)
		if _, err := ports.CreatePasskey(ctx, core.Passkey{
			UserID: stranger.ID, CredentialID: []byte("credential-" + owner.Username), PublicKey: []byte("k"), AAGUID: []byte("aaguid"),
		}); !errors.Is(err, core.ErrPasskeyAlreadyRegistered) {
			t.Errorf("re-registering to another account = %v, want ErrPasskeyAlreadyRegistered", err)
		}
	})

	t.Run("lists a user's own credentials and nothing else", func(t *testing.T) {
		owner := newUser(t, ports)
		empty, err := ports.ListPasskeysByUser(ctx, owner.ID)
		if err != nil {
			t.Fatal(err)
		}
		if empty == nil {
			t.Error("ListPasskeysByUser returned nil for a user with none")
		}

		mine := newPasskey(t, owner.ID, owner.Username)
		stranger := newUser(t, ports)
		newPasskey(t, stranger.ID, stranger.Username)

		listed, err := ports.ListPasskeysByUser(ctx, owner.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(listed) != 1 || listed[0].ID != mine.ID {
			t.Errorf("ListPasskeysByUser = %#v, want only the caller's credential", listed)
		}
	})

	// A passkey is a login credential, so reaching one that belongs to
	// somebody else would be a way to take over their account.
	t.Run("hides another user's credential from every method", func(t *testing.T) {
		owner := newUser(t, ports)
		stranger := newUser(t, ports)
		passkey := newPasskey(t, owner.ID, owner.Username)

		if _, err := ports.UpdatePasskeyDescription(ctx, stranger.ID, passkey.ID, "Hijacked"); !errors.Is(err, core.ErrPasskeyNotFound) {
			t.Errorf("UpdatePasskeyDescription as a stranger = %v, want ErrPasskeyNotFound", err)
		}
		if err := ports.UpdatePasskeyCredential(ctx, stranger.ID, passkey.CredentialID, 99, false); !errors.Is(err, core.ErrPasskeyNotFound) {
			t.Errorf("UpdatePasskeyCredential as a stranger = %v, want ErrPasskeyNotFound", err)
		}
		if err := ports.DeletePasskey(ctx, stranger.ID, passkey.ID); !errors.Is(err, core.ErrPasskeyNotFound) {
			t.Errorf("DeletePasskey as a stranger = %v, want ErrPasskeyNotFound", err)
		}

		survivors, err := ports.ListPasskeysByUser(ctx, owner.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(survivors) != 1 || survivors[0].Description != "Phone" || survivors[0].SignCount != 1 {
			t.Errorf("the owner's credential was changed by a stranger: %#v", survivors)
		}
	})

	t.Run("reports an unknown credential rather than a zero one", func(t *testing.T) {
		owner := newUser(t, ports)
		if _, err := ports.UpdatePasskeyDescription(ctx, owner.ID, UnknownID, "Name"); !errors.Is(err, core.ErrPasskeyNotFound) {
			t.Errorf("UpdatePasskeyDescription error = %v, want ErrPasskeyNotFound", err)
		}
		if err := ports.UpdatePasskeyCredential(ctx, owner.ID, []byte("no-such-credential"), 1, false); !errors.Is(err, core.ErrPasskeyNotFound) {
			t.Errorf("UpdatePasskeyCredential error = %v, want ErrPasskeyNotFound", err)
		}
		if err := ports.DeletePasskey(ctx, owner.ID, UnknownID); !errors.Is(err, core.ErrPasskeyNotFound) {
			t.Errorf("DeletePasskey error = %v, want ErrPasskeyNotFound", err)
		}
	})

	// The signature counter is the store's cloned-authenticator defense, so
	// each login has to be able to record the value it saw.
	t.Run("records a new signature count and backup state", func(t *testing.T) {
		owner := newUser(t, ports)
		passkey := newPasskey(t, owner.ID, owner.Username)

		if err := ports.UpdatePasskeyCredential(ctx, owner.ID, passkey.CredentialID, 42, true); err != nil {
			t.Fatal(err)
		}
		listed, err := ports.ListPasskeysByUser(ctx, owner.ID)
		if err != nil || len(listed) != 1 {
			t.Fatalf("ListPasskeysByUser = %#v, %v", listed, err)
		}
		if listed[0].SignCount != 42 || !listed[0].BackupState {
			t.Errorf("after the update the credential is %#v, want SignCount 42 and BackupState set", listed[0])
		}
	})

	t.Run("renames and deletes a credential", func(t *testing.T) {
		owner := newUser(t, ports)
		passkey := newPasskey(t, owner.ID, owner.Username)

		renamed, err := ports.UpdatePasskeyDescription(ctx, owner.ID, passkey.ID, "Laptop")
		if err != nil {
			t.Fatal(err)
		}
		if renamed.ID != passkey.ID || renamed.Description != "Laptop" {
			t.Errorf("renamed = %#v, want the same credential under the new description", renamed)
		}

		if err := ports.DeletePasskey(ctx, owner.ID, passkey.ID); err != nil {
			t.Fatal(err)
		}
		remaining, err := ports.ListPasskeysByUser(ctx, owner.ID)
		if err != nil || len(remaining) != 0 {
			t.Errorf("after delete the user holds %#v, %v", remaining, err)
		}
		// Deleting frees the credential ID, so the authenticator can be
		// enrolled again.
		if _, err := ports.CreatePasskey(ctx, core.Passkey{
			UserID: owner.ID, CredentialID: passkey.CredentialID, PublicKey: []byte("k"), AAGUID: []byte("aaguid"),
		}); err != nil {
			t.Errorf("re-enrolling a deleted credential = %v, want it allowed", err)
		}
	})
}

// RunPasskeyChallengeStore checks the port holding one-time WebAuthn ceremony
// state. Consumption is what makes a challenge single-use, and that is the
// whole point of the port: a challenge that could be redeemed twice would let
// a captured ceremony be replayed.
//
// Expiry is deliberately not enforced on read. The store hands back what it
// removed and the caller compares ExpiresAt, so a caller can tell an expired
// challenge from an absent one; sweeping expired rows is a separate method.
func RunPasskeyChallengeStore(t *testing.T, ports Ports) {
	ctx := context.Background()

	create := func(t *testing.T, id string, userID *string, expires time.Time) {
		t.Helper()
		err := ports.CreatePasskeyChallenge(ctx, core.PasskeyChallenge{
			ID: id, UserID: userID, SessionData: []byte("session-" + id),
			Kind: core.PasskeyChallengeKind("registration"), ExpiresAt: expires,
		})
		if err != nil {
			t.Fatalf("creating fixture challenge: %v", err)
		}
	}

	t.Run("round trips a challenge and consumes it once", func(t *testing.T) {
		user := newUser(t, ports)
		id := testUUID(user.Username)
		expires := time.Now().Add(time.Minute).UTC().Truncate(time.Microsecond)
		create(t, id, &user.ID, expires)

		consumed, err := ports.ConsumePasskeyChallenge(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if consumed.ID != id || consumed.UserID == nil || *consumed.UserID != user.ID {
			t.Errorf("consumed %#v, want the challenge that was written", consumed)
		}
		if string(consumed.SessionData) != "session-"+id {
			t.Errorf("SessionData = %q, want the bytes written", consumed.SessionData)
		}
		if consumed.Kind != core.PasskeyChallengeKind("registration") || !consumed.ExpiresAt.Equal(expires) {
			t.Errorf("consumed %#v, want the kind and expiry written", consumed)
		}

		// The replay defense: the second attempt finds nothing.
		if _, err := ports.ConsumePasskeyChallenge(ctx, id); !errors.Is(err, core.ErrInvalidPasskeyChallenge) {
			t.Errorf("consuming twice = %v, want ErrInvalidPasskeyChallenge", err)
		}
	})

	// A login ceremony begins before anyone is identified, so the challenge
	// has no user until it is finished.
	t.Run("holds a challenge with no user yet", func(t *testing.T) {
		id := testUUID("anonymous-challenge")
		create(t, id, nil, time.Now().Add(time.Minute))

		consumed, err := ports.ConsumePasskeyChallenge(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if consumed.UserID != nil {
			t.Errorf("UserID = %v, want nil for a login ceremony", *consumed.UserID)
		}
	})

	t.Run("reports an unknown challenge rather than a zero one", func(t *testing.T) {
		if _, err := ports.ConsumePasskeyChallenge(ctx, UnknownID); !errors.Is(err, core.ErrInvalidPasskeyChallenge) {
			t.Errorf("ConsumePasskeyChallenge error = %v, want ErrInvalidPasskeyChallenge", err)
		}
	})

	// Expiry is the caller's comparison, and the sweep is what stops the
	// table growing without bound.
	t.Run("returns an expired challenge but sweeps it on request", func(t *testing.T) {
		expired := testUUID("expired-challenge")
		live := testUUID("live-challenge")
		cutoff := time.Now()
		create(t, expired, nil, cutoff.Add(-time.Minute))
		create(t, live, nil, cutoff.Add(time.Hour))

		if err := ports.DeleteExpiredPasskeyChallenges(ctx, cutoff); err != nil {
			t.Fatal(err)
		}
		if _, err := ports.ConsumePasskeyChallenge(ctx, expired); !errors.Is(err, core.ErrInvalidPasskeyChallenge) {
			t.Errorf("an expired challenge survived the sweep: %v", err)
		}
		if _, err := ports.ConsumePasskeyChallenge(ctx, live); err != nil {
			t.Errorf("the sweep took a live challenge with it: %v", err)
		}
	})
}
