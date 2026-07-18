package core

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"
)

type fakePasskeyStore struct {
	passkeys           []Passkey
	listedUserID       string
	renamedUserID      string
	renamedPasskeyID   string
	renamedDescription string
	deletedUserID      string
	deletedPasskeyID   string
}

func (s *fakePasskeyStore) CreatePasskey(_ context.Context, passkey Passkey) (Passkey, error) {
	s.passkeys = append(s.passkeys, passkey)
	return passkey, nil
}

func (s *fakePasskeyStore) ListPasskeysByUser(_ context.Context, userID string) ([]Passkey, error) {
	s.listedUserID = userID
	return append([]Passkey(nil), s.passkeys...), nil
}

func (s *fakePasskeyStore) UpdatePasskeyDescription(_ context.Context, userID, passkeyID, description string) (Passkey, error) {
	s.renamedUserID = userID
	s.renamedPasskeyID = passkeyID
	s.renamedDescription = description
	for i := range s.passkeys {
		if s.passkeys[i].ID == passkeyID && s.passkeys[i].UserID == userID {
			s.passkeys[i].Description = description
			return s.passkeys[i], nil
		}
	}
	return Passkey{}, ErrPasskeyNotFound
}

func (s *fakePasskeyStore) UpdatePasskeyCredential(context.Context, string, []byte, uint32, bool) error {
	return nil
}

func (s *fakePasskeyStore) DeletePasskey(_ context.Context, userID, passkeyID string) error {
	s.deletedUserID = userID
	s.deletedPasskeyID = passkeyID
	for i := range s.passkeys {
		if s.passkeys[i].ID == passkeyID && s.passkeys[i].UserID == userID {
			s.passkeys = append(s.passkeys[:i], s.passkeys[i+1:]...)
			return nil
		}
	}
	return ErrPasskeyNotFound
}

type fakePasskeyChallengeStore struct {
	challenges    map[string]PasskeyChallenge
	cleanupBefore time.Time
}

func (s *fakePasskeyChallengeStore) CreatePasskeyChallenge(_ context.Context, challenge PasskeyChallenge) error {
	if s.challenges == nil {
		s.challenges = map[string]PasskeyChallenge{}
	}
	s.challenges[challenge.ID] = challenge
	return nil
}

func (s *fakePasskeyChallengeStore) ConsumePasskeyChallenge(_ context.Context, id string) (PasskeyChallenge, error) {
	challenge, ok := s.challenges[id]
	if !ok {
		return PasskeyChallenge{}, ErrInvalidPasskeyChallenge
	}
	delete(s.challenges, id)
	return challenge, nil
}

func (s *fakePasskeyChallengeStore) DeleteExpiredPasskeyChallenges(_ context.Context, before time.Time) error {
	s.cleanupBefore = before
	for id, challenge := range s.challenges {
		if !challenge.ExpiresAt.After(before) {
			delete(s.challenges, id)
		}
	}
	return nil
}

func newPasskeyTestWebAuthn(t *testing.T) *webauthn.WebAuthn {
	t.Helper()
	wan, err := webauthn.New(&webauthn.Config{
		RPDisplayName: "Logger4Life Test",
		RPID:          "localhost",
		RPOrigins:     []string{"http://localhost"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return wan
}

func TestBeginPasskeyActionsPersistTypedChallenges(t *testing.T) {
	const userID = "018f47a0-7b5c-7e8d-9f01-23456789abcd"
	users := &fakeUserStore{user: User{ID: userID, Username: "alice"}}
	passkeys := &fakePasskeyStore{}
	challenges := &fakePasskeyChallengeStore{}
	app := New(Config{Users: users, Passkeys: passkeys, Challenges: challenges, WebAuthn: newPasskeyTestWebAuthn(t)})

	registration, err := BeginPasskeyRegistration.Call(WithUserID(context.Background(), userID), app, BeginPasskeyRegistrationParams{})
	if err != nil {
		t.Fatal(err)
	}
	stored := challenges.challenges[registration.ChallengeID]
	if registration.Options == nil || stored.Kind != PasskeyRegistrationChallenge || stored.UserID == nil || *stored.UserID != userID {
		t.Fatalf("registration = %#v; challenge = %#v", registration, stored)
	}
	if stored.ExpiresAt.Before(time.Now()) || challenges.cleanupBefore.IsZero() {
		t.Fatalf("challenge expiry/cleanup not set: %#v", stored)
	}

	login, err := BeginPasskeyLogin.Call(context.Background(), app, BeginPasskeyLoginParams{})
	if err != nil {
		t.Fatal(err)
	}
	stored = challenges.challenges[login.ChallengeID]
	if login.Options == nil || stored.Kind != PasskeyLoginChallenge || stored.UserID != nil {
		t.Fatalf("login = %#v; challenge = %#v", login, stored)
	}
	if _, ok := Lookup("begin_passkey_registration"); !ok {
		t.Fatal("passkey registration is missing from the action catalog")
	}
}

func TestPasskeyManagementActionsUseAuthenticatedUser(t *testing.T) {
	const (
		userID    = "018f47a0-7b5c-7e8d-9f01-23456789abcd"
		passkeyID = "018f47a0-7b5c-7e8d-9f01-23456789abce"
	)
	createdAt := time.Now().UTC()
	passkeys := &fakePasskeyStore{passkeys: []Passkey{{ID: passkeyID, UserID: userID, Description: "Laptop", CreatedAt: createdAt}}}
	app := New(Config{Passkeys: passkeys, WebAuthn: newPasskeyTestWebAuthn(t)})
	ctx := WithUserID(context.Background(), userID)

	listed, err := ListPasskeys.Call(ctx, app, ListPasskeysParams{})
	if err != nil || len(listed) != 1 || listed[0].Description != "Laptop" || passkeys.listedUserID != userID {
		t.Fatalf("ListPasskeys() = %#v, %v", listed, err)
	}
	renamed, err := RenamePasskey.Call(ctx, app, RenamePasskeyParams{PasskeyID: passkeyID, Description: "  Phone  "})
	if err != nil || renamed.Description != "Phone" || passkeys.renamedUserID != userID || passkeys.renamedDescription != "Phone" {
		t.Fatalf("RenamePasskey() = %#v, %v", renamed, err)
	}
	if _, err := DeletePasskey.Call(ctx, app, DeletePasskeyParams{PasskeyID: passkeyID}); err != nil || passkeys.deletedUserID != userID {
		t.Fatalf("DeletePasskey() error = %v", err)
	}
	if _, err := ListPasskeys.Call(context.Background(), app, ListPasskeysParams{}); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("unauthenticated list error = %v", err)
	}
}

func TestFinishPasskeyRegistrationConsumesAndBindsChallenge(t *testing.T) {
	const (
		userID      = "018f47a0-7b5c-7e8d-9f01-23456789abcd"
		challengeID = "018f47a0-7b5c-7e8d-9f01-23456789abce"
		otherUserID = "018f47a0-7b5c-7e8d-9f01-23456789abcf"
	)
	sessionData, err := json.Marshal(webauthn.SessionData{Challenge: "test", Expires: time.Now().Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	challenges := &fakePasskeyChallengeStore{challenges: map[string]PasskeyChallenge{
		challengeID: {ID: challengeID, UserID: stringPointer(otherUserID), SessionData: sessionData, Kind: PasskeyRegistrationChallenge, ExpiresAt: time.Now().Add(time.Minute)},
	}}
	app := New(Config{
		Users:    &fakeUserStore{user: User{ID: userID, Username: "alice"}},
		Passkeys: &fakePasskeyStore{}, Challenges: challenges, WebAuthn: newPasskeyTestWebAuthn(t),
	})
	_, err = FinishPasskeyRegistration.Call(WithUserID(context.Background(), userID), app, FinishPasskeyRegistrationParams{
		ChallengeID: challengeID, Credential: json.RawMessage(`{}`),
	})
	if !errors.Is(err, ErrInvalidPasskeyChallenge) {
		t.Fatalf("mismatched challenge error = %v", err)
	}
	if _, ok := challenges.challenges[challengeID]; ok {
		t.Fatal("consumed challenge can be reused")
	}
}

func TestPasskeyParamsValidateDescriptionAndIDs(t *testing.T) {
	app := New(Config{})
	_, err := RenamePasskey.Call(context.Background(), app, RenamePasskeyParams{
		PasskeyID: "not-a-uuid", Description: strings.Repeat("x", maxPasskeyDescriptionLength+1),
	})
	var validationErr *ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("RenamePasskey() error = %T %v, want ValidationError", err, err)
	}
}

func stringPointer(value string) *string { return &value }
