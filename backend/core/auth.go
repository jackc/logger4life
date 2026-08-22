package core

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const SessionDuration = 30 * 24 * time.Hour

const sessionTokenLength = 32

// User is the public application identity. Password hashes deliberately live
// only in UserStore method values and can never be serialized as a User.
type User struct {
	ID       string  `json:"id"`
	Username string  `json:"username"`
	Email    *string `json:"email,omitempty"`
}

// Session is the server-side credential persisted for an authenticated user.
// Token contains raw random bytes; AuthSession is the client-facing form.
type Session struct {
	UserID    string    `json:"-"`
	Token     []byte    `json:"-"`
	ExpiresAt time.Time `json:"-"`
}

// AuthSession is returned by registration and login so an adapter can write
// the transport-specific session cookie.
type AuthSession struct {
	User      User      `json:"user"`
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

var (
	ErrUsernameTaken      = errors.New("username already taken")
	ErrEmailTaken         = errors.New("email already in use")
	ErrUserNotFound       = errors.New("user not found")
	ErrInvalidCredentials = errors.New("invalid username or password")
	ErrInvalidSession     = errors.New("invalid or expired session")
	ErrIncorrectPassword  = errors.New("current password is incorrect")
)

// UserStore is the driven persistence port for users and password
// credentials. Password hashes are kept separate from the public User type.
type UserStore interface {
	CreateUser(context.Context, string, *string, string) (User, error)
	GetUserByUsername(context.Context, string) (User, string, error)
	GetUserByID(context.Context, string) (User, error)
	UpdateUserEmail(context.Context, string, *string) (User, error)
	GetUserPasswordHash(context.Context, string) (string, error)
	UpdateUserPasswordHash(context.Context, string, string) error
}

// SessionStore is the driven persistence port for login sessions.
type SessionStore interface {
	CreateSession(context.Context, Session) error
	GetUserBySessionToken(context.Context, []byte) (User, error)
	DeleteSessionByToken(context.Context, []byte) error
}

var usernamePattern = regexp.MustCompile(`^[a-zA-Z0-9_]+$`)

func normalizeEmail(email *string) *string {
	if email == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*email)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func validatePassword(password, field string) error {
	if len(password) < 8 {
		return fmt.Errorf("%s must be at least 8 characters", field)
	}
	if len(password) > 72 {
		return fmt.Errorf("%s must be at most 72 characters", field)
	}
	return nil
}

func newSession(userID string) (Session, error) {
	token := make([]byte, sessionTokenLength)
	if _, err := rand.Read(token); err != nil {
		return Session{}, err
	}
	return Session{UserID: userID, Token: token, ExpiresAt: time.Now().Add(SessionDuration)}, nil
}

func authSession(user User, session Session) AuthSession {
	return AuthSession{User: user, Token: hex.EncodeToString(session.Token), ExpiresAt: session.ExpiresAt}
}

type RegisterUserParams struct {
	Username string  `json:"username"`
	Email    *string `json:"email,omitempty"`
	Password string  `json:"password"`
}

func (p *RegisterUserParams) Validate() error {
	p.Username = strings.TrimSpace(p.Username)
	if len(p.Username) < 1 || len(p.Username) > 30 || !usernamePattern.MatchString(p.Username) {
		return errors.New("username must be 1-30 letters, digits, or underscores")
	}
	p.Email = normalizeEmail(p.Email)
	return validatePassword(p.Password, "password")
}

var RegisterUser = Define(ActionDef[RegisterUserParams, AuthSession]{
	Name: "register_user", Public: true, Description: "Register a user and start a session.", Mutating: true,
	Handler: func(ctx context.Context, c *Core, p RegisterUserParams) (AuthSession, error) {
		hash, err := bcrypt.GenerateFromPassword([]byte(p.Password), bcrypt.DefaultCost)
		if err != nil {
			return AuthSession{}, err
		}
		var user User
		var session Session
		err = c.tx.InTx(ctx, func(txCtx context.Context) error {
			var err error
			user, err = c.users.CreateUser(txCtx, p.Username, p.Email, string(hash))
			if err != nil {
				return err
			}
			session, err = newSession(user.ID)
			if err != nil {
				return err
			}
			return c.sessions.CreateSession(txCtx, session)
		})
		if err != nil {
			return AuthSession{}, fmt.Errorf("register user: %w", err)
		}
		return authSession(user, session), nil
	},
})

type LoginWithPasswordParams struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

var LoginWithPassword = Define(ActionDef[LoginWithPasswordParams, AuthSession]{
	Name: "login_with_password", Public: true, Description: "Log in with a username and password.", Mutating: true,
	Handler: func(ctx context.Context, c *Core, p LoginWithPasswordParams) (AuthSession, error) {
		user, hash, err := c.users.GetUserByUsername(ctx, p.Username)
		if errors.Is(err, ErrUserNotFound) {
			return AuthSession{}, ErrInvalidCredentials
		}
		if err != nil {
			return AuthSession{}, err
		}
		if bcrypt.CompareHashAndPassword([]byte(hash), []byte(p.Password)) != nil {
			return AuthSession{}, ErrInvalidCredentials
		}
		session, err := newSession(user.ID)
		if err != nil {
			return AuthSession{}, err
		}
		if err := c.sessions.CreateSession(ctx, session); err != nil {
			return AuthSession{}, err
		}
		return authSession(user, session), nil
	},
})

type AuthenticateSessionParams struct {
	Token string `json:"token"`
}

var AuthenticateSession = Define(ActionDef[AuthenticateSessionParams, User]{
	Name: "authenticate_session", Public: true, Description: "Resolve a session token to its user.",
	Handler: func(ctx context.Context, c *Core, p AuthenticateSessionParams) (User, error) {
		token, err := hex.DecodeString(p.Token)
		if err != nil || len(token) == 0 {
			return User{}, ErrInvalidSession
		}
		return c.sessions.GetUserBySessionToken(ctx, token)
	},
})

type LogoutParams struct {
	Token string `json:"token"`
}

var Logout = Define(ActionDef[LogoutParams, struct{}]{
	Name: "logout", Public: true, Description: "Delete a session.", Mutating: true,
	Handler: func(ctx context.Context, c *Core, p LogoutParams) (struct{}, error) {
		token, err := hex.DecodeString(p.Token)
		if err != nil || len(token) == 0 {
			return struct{}{}, nil
		}
		return struct{}{}, c.sessions.DeleteSessionByToken(ctx, token)
	},
})

type GetProfileParams struct{}

var GetProfile = Define(ActionDef[GetProfileParams, User]{
	Name: "get_profile", Description: "Get the current user's profile.",
	Handler: func(ctx context.Context, c *Core, _ GetProfileParams) (User, error) {
		userID, err := requiredUser(ctx)
		if err != nil {
			return User{}, err
		}
		return c.users.GetUserByID(ctx, userID)
	},
})

type ChangeEmailParams struct {
	Email *string `json:"email"`
}

func (p *ChangeEmailParams) Validate() error {
	p.Email = normalizeEmail(p.Email)
	return nil
}

var ChangeEmail = Define(ActionDef[ChangeEmailParams, User]{
	Name: "change_email", Description: "Change or clear the current user's email.", Mutating: true,
	Handler: func(ctx context.Context, c *Core, p ChangeEmailParams) (User, error) {
		userID, err := requiredUser(ctx)
		if err != nil {
			return User{}, err
		}
		return c.users.UpdateUserEmail(ctx, userID, p.Email)
	},
})

type ChangePasswordParams struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

func (p *ChangePasswordParams) Validate() error {
	return validatePassword(p.NewPassword, "new password")
}

var ChangePassword = Define(ActionDef[ChangePasswordParams, struct{}]{
	Name: "change_password", Description: "Change the current user's password.", Mutating: true,
	Handler: func(ctx context.Context, c *Core, p ChangePasswordParams) (struct{}, error) {
		userID, err := requiredUser(ctx)
		if err != nil {
			return struct{}{}, err
		}
		hash, err := c.users.GetUserPasswordHash(ctx, userID)
		if err != nil {
			return struct{}{}, err
		}
		if bcrypt.CompareHashAndPassword([]byte(hash), []byte(p.CurrentPassword)) != nil {
			return struct{}{}, ErrIncorrectPassword
		}
		newHash, err := bcrypt.GenerateFromPassword([]byte(p.NewPassword), bcrypt.DefaultCost)
		if err != nil {
			return struct{}{}, err
		}
		return struct{}{}, c.users.UpdateUserPasswordHash(ctx, userID, string(newHash))
	},
})
