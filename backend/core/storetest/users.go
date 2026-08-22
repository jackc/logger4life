package storetest

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/logger4life/backend/core"
)

// RunUserStore checks the port that registration, login, and account changes
// depend on.
func RunUserStore(t *testing.T, ports Ports) {
	ctx := context.Background()

	t.Run("round trips a user with and without an email", func(t *testing.T) {
		user := newUser(t, ports)
		if user.ID == "" {
			t.Fatal("CreateUser returned no ID")
		}
		if user.Email != nil {
			t.Errorf("Email = %v, want nil; a user registered without one has none", *user.Email)
		}

		address := user.Username + "@example.com"
		withEmail, err := ports.CreateUser(ctx, user.Username+"_b", &address, "hash")
		if err != nil {
			t.Fatal(err)
		}
		if withEmail.Email == nil || *withEmail.Email != address {
			t.Errorf("Email = %v, want %q", withEmail.Email, address)
		}
	})

	// Usernames and emails are matched case-insensitively, so a second account
	// cannot be opened by changing the capitalization of an existing one.
	t.Run("refuses a duplicate username or email whatever its case", func(t *testing.T) {
		user := newUser(t, ports)
		if _, err := ports.CreateUser(ctx, strings.ToUpper(user.Username), nil, "hash"); !errors.Is(err, core.ErrUsernameTaken) {
			t.Errorf("re-registering %q uppercased = %v, want ErrUsernameTaken", user.Username, err)
		}

		address := user.Username + "@example.com"
		if _, err := ports.UpdateUserEmail(ctx, user.ID, &address); err != nil {
			t.Fatal(err)
		}
		upper := strings.ToUpper(address)
		other := newUser(t, ports)
		if _, err := ports.UpdateUserEmail(ctx, other.ID, &upper); !errors.Is(err, core.ErrEmailTaken) {
			t.Errorf("claiming %q uppercased = %v, want ErrEmailTaken", address, err)
		}
	})

	// Login looks a user up by whatever they typed, so the lookup has to match
	// the case-insensitive index the username is stored under.
	t.Run("finds a user by username in any case and returns the hash", func(t *testing.T) {
		user := newUser(t, ports)
		found, hash, err := ports.GetUserByUsername(ctx, strings.ToUpper(user.Username))
		if err != nil {
			t.Fatal(err)
		}
		if found.ID != user.ID {
			t.Errorf("found %q, want %q", found.ID, user.ID)
		}
		if hash != "fixture-hash" {
			t.Errorf("hash = %q, want the stored hash", hash)
		}
		if found.Username != user.Username {
			t.Errorf("Username = %q, want the stored spelling %q", found.Username, user.Username)
		}
	})

	t.Run("reports a missing user rather than an empty one", func(t *testing.T) {
		if _, _, err := ports.GetUserByUsername(ctx, UserPrefix+"absent"); !errors.Is(err, core.ErrUserNotFound) {
			t.Errorf("GetUserByUsername error = %v, want ErrUserNotFound", err)
		}
		if _, err := ports.GetUserByID(ctx, UnknownID); !errors.Is(err, core.ErrUserNotFound) {
			t.Errorf("GetUserByID error = %v, want ErrUserNotFound", err)
		}
		if _, err := ports.GetUserPasswordHash(ctx, UnknownID); !errors.Is(err, core.ErrUserNotFound) {
			t.Errorf("GetUserPasswordHash error = %v, want ErrUserNotFound", err)
		}
		if err := ports.UpdateUserPasswordHash(ctx, UnknownID, "hash"); !errors.Is(err, core.ErrUserNotFound) {
			t.Errorf("UpdateUserPasswordHash error = %v, want ErrUserNotFound", err)
		}
		if _, err := ports.UpdateUserEmail(ctx, UnknownID, nil); !errors.Is(err, core.ErrUserNotFound) {
			t.Errorf("UpdateUserEmail error = %v, want ErrUserNotFound", err)
		}
	})

	// Clearing an address has to be distinguishable from leaving it alone,
	// which is why the port takes a pointer rather than a string.
	t.Run("sets and clears an email", func(t *testing.T) {
		user := newUser(t, ports)
		address := user.Username + "@example.com"
		if _, err := ports.UpdateUserEmail(ctx, user.ID, &address); err != nil {
			t.Fatal(err)
		}
		cleared, err := ports.UpdateUserEmail(ctx, user.ID, nil)
		if err != nil {
			t.Fatal(err)
		}
		if cleared.Email != nil {
			t.Errorf("Email = %q after clearing, want nil", *cleared.Email)
		}
		reread, err := ports.GetUserByID(ctx, user.ID)
		if err != nil || reread.Email != nil {
			t.Errorf("GetUserByID after clearing = %#v, %v", reread.Email, err)
		}
	})

	t.Run("replaces a password hash", func(t *testing.T) {
		user := newUser(t, ports)
		if err := ports.UpdateUserPasswordHash(ctx, user.ID, "second-hash"); err != nil {
			t.Fatal(err)
		}
		hash, err := ports.GetUserPasswordHash(ctx, user.ID)
		if err != nil || hash != "second-hash" {
			t.Errorf("hash = %q, %v; want the replacement", hash, err)
		}
	})
}

// RunSessionStore checks the port every authenticated request passes through.
func RunSessionStore(t *testing.T, ports Ports) {
	ctx := context.Background()

	newSession := func(t *testing.T, userID string, token []byte, expires time.Time) {
		t.Helper()
		if err := ports.CreateSession(ctx, core.Session{UserID: userID, Token: token, ExpiresAt: expires}); err != nil {
			t.Fatalf("creating session: %v", err)
		}
	}

	t.Run("resolves a live token to its user", func(t *testing.T) {
		user := newUser(t, ports)
		token := []byte(user.Username + "-live")
		newSession(t, user.ID, token, time.Now().Add(time.Hour))

		found, err := ports.GetUserBySessionToken(ctx, token)
		if err != nil {
			t.Fatal(err)
		}
		if found.ID != user.ID || found.Username != user.Username {
			t.Errorf("resolved to %#v, want %s", found, user.ID)
		}
	})

	// An expired session must be indistinguishable from no session at all;
	// expiry is enforced here rather than by whoever reads the cookie.
	t.Run("refuses an expired token", func(t *testing.T) {
		user := newUser(t, ports)
		token := []byte(user.Username + "-expired")
		newSession(t, user.ID, token, time.Now().Add(-time.Minute))

		if _, err := ports.GetUserBySessionToken(ctx, token); !errors.Is(err, core.ErrInvalidSession) {
			t.Errorf("expired token error = %v, want ErrInvalidSession", err)
		}
	})

	t.Run("refuses an unknown token", func(t *testing.T) {
		if _, err := ports.GetUserBySessionToken(ctx, []byte("no-such-token")); !errors.Is(err, core.ErrInvalidSession) {
			t.Errorf("unknown token error = %v, want ErrInvalidSession", err)
		}
	})

	t.Run("deletes a session and tolerates deleting it twice", func(t *testing.T) {
		user := newUser(t, ports)
		token := []byte(user.Username + "-logout")
		newSession(t, user.ID, token, time.Now().Add(time.Hour))

		if err := ports.DeleteSessionByToken(ctx, token); err != nil {
			t.Fatal(err)
		}
		if _, err := ports.GetUserBySessionToken(ctx, token); !errors.Is(err, core.ErrInvalidSession) {
			t.Errorf("deleted token error = %v, want ErrInvalidSession", err)
		}
		// Logging out with a cookie the server has already forgotten is a
		// normal thing for a browser to do, and must not be an error.
		if err := ports.DeleteSessionByToken(ctx, token); err != nil {
			t.Errorf("deleting an already-deleted session = %v, want nil", err)
		}
	})
}
