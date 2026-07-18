package core

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type authTxKey struct{}

type fakeAuthTx struct {
	called bool
}

func (tx *fakeAuthTx) InTx(ctx context.Context, fn func(context.Context) error) error {
	tx.called = true
	return fn(context.WithValue(ctx, authTxKey{}, true))
}

type fakeUserStore struct {
	user              User
	passwordHash      string
	createdUsername   string
	createdEmail      *string
	createdInTx       bool
	updatedEmail      *string
	updatedPassword   string
	getByUsernameErr  error
	getByIDErr        error
	updateEmailErr    error
	getPasswordErr    error
	updatePasswordErr error
}

func (s *fakeUserStore) CreateUser(ctx context.Context, username string, email *string, hash string) (User, error) {
	s.createdUsername = username
	s.createdEmail = email
	s.passwordHash = hash
	s.createdInTx, _ = ctx.Value(authTxKey{}).(bool)
	if s.user.ID == "" {
		s.user = User{ID: "user-1", Username: username, Email: email}
	}
	return s.user, nil
}

func (s *fakeUserStore) GetUserByUsername(context.Context, string) (User, string, error) {
	return s.user, s.passwordHash, s.getByUsernameErr
}

func (s *fakeUserStore) GetUserByID(context.Context, string) (User, error) {
	return s.user, s.getByIDErr
}

func (s *fakeUserStore) UpdateUserEmail(_ context.Context, _ string, email *string) (User, error) {
	s.updatedEmail = email
	s.user.Email = email
	return s.user, s.updateEmailErr
}

func (s *fakeUserStore) GetUserPasswordHash(context.Context, string) (string, error) {
	return s.passwordHash, s.getPasswordErr
}

func (s *fakeUserStore) UpdateUserPasswordHash(_ context.Context, _ string, hash string) error {
	s.updatedPassword = hash
	return s.updatePasswordErr
}

type fakeSessionStore struct {
	session     Session
	createdInTx bool
	user        User
	getToken    []byte
	deleted     []byte
	err         error
}

func (s *fakeSessionStore) CreateSession(ctx context.Context, session Session) error {
	s.session = session
	s.createdInTx, _ = ctx.Value(authTxKey{}).(bool)
	return s.err
}

func (s *fakeSessionStore) GetUserBySessionToken(_ context.Context, token []byte) (User, error) {
	s.getToken = append([]byte(nil), token...)
	return s.user, s.err
}

func (s *fakeSessionStore) DeleteSessionByToken(_ context.Context, token []byte) error {
	s.deleted = append([]byte(nil), token...)
	return s.err
}

func TestRegisterUserValidatesAndCreatesUserAndSessionInTransaction(t *testing.T) {
	users := &fakeUserStore{}
	sessions := &fakeSessionStore{}
	tx := &fakeAuthTx{}
	app := New(Config{Users: users, Sessions: sessions, Tx: tx})
	email := "  alice@example.com  "

	result, err := RegisterUser.Call(context.Background(), app, RegisterUserParams{
		Username: "  alice  ", Email: &email, Password: "password123",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !tx.called || !users.createdInTx || !sessions.createdInTx {
		t.Fatal("registration writes did not use the transactor context")
	}
	if users.createdUsername != "alice" || users.createdEmail == nil || *users.createdEmail != "alice@example.com" {
		t.Fatalf("normalized user = %q, %#v", users.createdUsername, users.createdEmail)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(users.passwordHash), []byte("password123")); err != nil {
		t.Fatalf("stored password is not a bcrypt hash: %v", err)
	}
	if len(sessions.session.Token) != sessionTokenLength || sessions.session.UserID != "user-1" {
		t.Fatalf("session = %#v", sessions.session)
	}
	decoded, err := hex.DecodeString(result.Token)
	if err != nil || string(decoded) != string(sessions.session.Token) {
		t.Fatal("auth result does not contain the persisted token")
	}
	if time.Until(result.ExpiresAt) < SessionDuration-time.Minute {
		t.Fatalf("session expiry = %v", result.ExpiresAt)
	}
	encoded, err := json.Marshal(result.User)
	if err != nil || strings.Contains(string(encoded), "password") || strings.Contains(string(encoded), users.passwordHash) {
		t.Fatalf("public user leaks a password hash: %s", encoded)
	}
}

func TestRegisterUserPasswordAndUsernamePolicy(t *testing.T) {
	app := New(Config{})
	for _, params := range []RegisterUserParams{
		{Username: "", Password: "password123"},
		{Username: "not valid", Password: "password123"},
		{Username: "alice", Password: "short"},
		{Username: "alice", Password: strings.Repeat("x", 73)},
	} {
		if _, err := RegisterUser.Call(context.Background(), app, params); err == nil {
			t.Fatalf("RegisterUser(%#v) unexpectedly succeeded", params)
		} else {
			var validationErr *ValidationError
			if !errors.As(err, &validationErr) {
				t.Fatalf("error = %T %v, want ValidationError", err, err)
			}
		}
	}
}

func TestLoginWithPasswordCreatesSessionAndHidesCredentialFailures(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("password123"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	users := &fakeUserStore{user: User{ID: "user-1", Username: "Alice"}, passwordHash: string(hash)}
	sessions := &fakeSessionStore{}
	app := New(Config{Users: users, Sessions: sessions})

	result, err := LoginWithPassword.Call(context.Background(), app, LoginWithPasswordParams{Username: "alice", Password: "password123"})
	if err != nil || result.User.ID != "user-1" || sessions.session.UserID != "user-1" {
		t.Fatalf("LoginWithPassword() = %#v, %v", result, err)
	}
	if _, err := LoginWithPassword.Call(context.Background(), app, LoginWithPasswordParams{Username: "alice", Password: "wrong"}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong-password error = %v", err)
	}
	users.getByUsernameErr = ErrUserNotFound
	if _, err := LoginWithPassword.Call(context.Background(), app, LoginWithPasswordParams{Username: "missing", Password: "anything"}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("missing-user error = %v", err)
	}
}

func TestSessionAuthenticationAndLogoutDecodeTokens(t *testing.T) {
	sessions := &fakeSessionStore{user: User{ID: "user-1"}}
	app := New(Config{Sessions: sessions})

	user, err := AuthenticateSession.Call(context.Background(), app, AuthenticateSessionParams{Token: "0102"})
	if err != nil || user.ID != "user-1" || hex.EncodeToString(sessions.getToken) != "0102" {
		t.Fatalf("AuthenticateSession() = %#v, %v; token=%x", user, err, sessions.getToken)
	}
	if _, err := AuthenticateSession.Call(context.Background(), app, AuthenticateSessionParams{Token: "invalid"}); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("invalid token error = %v", err)
	}
	if _, err := Logout.Call(context.Background(), app, LogoutParams{Token: "aabb"}); err != nil || hex.EncodeToString(sessions.deleted) != "aabb" {
		t.Fatalf("Logout() error = %v; token=%x", err, sessions.deleted)
	}
	before := append([]byte(nil), sessions.deleted...)
	if _, err := Logout.Call(context.Background(), app, LogoutParams{Token: "bad-token"}); err != nil || string(before) != string(sessions.deleted) {
		t.Fatalf("invalid-token logout = %v; token=%x", err, sessions.deleted)
	}
}

func TestProfileEmailAndPasswordActionsUseAuthenticatedUser(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("password123"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	users := &fakeUserStore{user: User{ID: "user-1", Username: "alice"}, passwordHash: string(hash)}
	app := New(Config{Users: users})
	ctx := WithUserID(context.Background(), "user-1")

	if user, err := GetProfile.Call(ctx, app, GetProfileParams{}); err != nil || user.ID != "user-1" {
		t.Fatalf("GetProfile() = %#v, %v", user, err)
	}
	empty := "  "
	if user, err := ChangeEmail.Call(ctx, app, ChangeEmailParams{Email: &empty}); err != nil || user.Email != nil || users.updatedEmail != nil {
		t.Fatalf("ChangeEmail() = %#v, %v", user, err)
	}
	if _, err := ChangePassword.Call(ctx, app, ChangePasswordParams{CurrentPassword: "wrong", NewPassword: "newpassword456"}); !errors.Is(err, ErrIncorrectPassword) {
		t.Fatalf("wrong current password error = %v", err)
	}
	if _, err := ChangePassword.Call(ctx, app, ChangePasswordParams{CurrentPassword: "password123", NewPassword: "newpassword456"}); err != nil {
		t.Fatal(err)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(users.updatedPassword), []byte("newpassword456")); err != nil {
		t.Fatalf("new password was not hashed: %v", err)
	}
	if _, err := GetProfile.Call(context.Background(), app, GetProfileParams{}); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("unauthenticated profile error = %v", err)
	}
}
