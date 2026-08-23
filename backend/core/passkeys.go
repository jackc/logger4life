package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/gofrs/uuid/v5"
)

const maxPasskeyDescriptionLength = 100
const passkeyChallengeDuration = 5 * time.Minute

type PasskeyChallengeKind string

const (
	PasskeyRegistrationChallenge PasskeyChallengeKind = "registration"
	PasskeyLoginChallenge        PasskeyChallengeKind = "login"
)

// Passkey is the complete server-side credential record. Its credential
// material is deliberately excluded from PasskeyInfo, the browser-facing
// management view.
type Passkey struct {
	ID             string
	UserID         string
	CredentialID   []byte
	PublicKey      []byte
	AAGUID         []byte
	SignCount      uint32
	BackupEligible bool
	BackupState    bool
	Description    string
	CreatedAt      time.Time
}

type PasskeyInfo struct {
	ID          string    `json:"id"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
}

type PasskeyChallenge struct {
	ID          string
	UserID      *string
	SessionData []byte
	Kind        PasskeyChallengeKind
	ExpiresAt   time.Time
}

var (
	ErrPasskeysDisabled          = errors.New("passkeys are not configured")
	ErrPasskeyNotFound           = errors.New("passkey not found")
	ErrPasskeyAlreadyRegistered  = errors.New("passkey is already registered")
	ErrInvalidPasskeyChallenge   = errors.New("invalid or expired challenge")
	ErrPasskeyVerificationFailed = errors.New("passkey verification failed")
)

// PasskeyStore is the driven persistence port for long-lived WebAuthn
// credentials.
type PasskeyStore interface {
	CreatePasskey(context.Context, Passkey) (Passkey, error)
	ListPasskeysByUser(context.Context, string) ([]Passkey, error)
	UpdatePasskeyDescription(context.Context, string, string, string) (Passkey, error)
	UpdatePasskeyCredential(context.Context, string, []byte, uint32, bool) error
	DeletePasskey(context.Context, string, string) error
}

// PasskeyChallengeStore is the driven persistence port for short-lived,
// one-time WebAuthn ceremony state.
type PasskeyChallengeStore interface {
	CreatePasskeyChallenge(context.Context, PasskeyChallenge) error
	ConsumePasskeyChallenge(context.Context, string) (PasskeyChallenge, error)
	DeleteExpiredPasskeyChallenges(context.Context, time.Time) error
}

type webAuthnUser struct {
	user        User
	id          []byte
	credentials []webauthn.Credential
}

func (u *webAuthnUser) WebAuthnID() []byte                         { return u.id }
func (u *webAuthnUser) WebAuthnName() string                       { return u.user.Username }
func (u *webAuthnUser) WebAuthnDisplayName() string                { return u.user.Username }
func (u *webAuthnUser) WebAuthnCredentials() []webauthn.Credential { return u.credentials }

func loadWebAuthnUser(ctx context.Context, c *Core, userID string) (*webAuthnUser, error) {
	user, err := c.users.GetUserByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	uid, err := uuid.FromString(user.ID)
	if err != nil {
		return nil, fmt.Errorf("parse WebAuthn user ID: %w", err)
	}
	passkeys, err := c.passkeys.ListPasskeysByUser(ctx, user.ID)
	if err != nil {
		return nil, err
	}
	credentials := make([]webauthn.Credential, len(passkeys))
	for i, passkey := range passkeys {
		credentials[i] = webauthn.Credential{
			ID:        append([]byte(nil), passkey.CredentialID...),
			PublicKey: append([]byte(nil), passkey.PublicKey...),
			Flags: webauthn.CredentialFlags{
				BackupEligible: passkey.BackupEligible,
				BackupState:    passkey.BackupState,
			},
			Authenticator: webauthn.Authenticator{
				AAGUID:    append([]byte(nil), passkey.AAGUID...),
				SignCount: passkey.SignCount,
			},
		}
	}
	return &webAuthnUser{user: user, id: uid.Bytes(), credentials: credentials}, nil
}

func currentPasskeyUser(ctx context.Context, c *Core) (*webAuthnUser, error) {
	userID, err := requiredUser(ctx)
	if err != nil {
		return nil, err
	}
	return loadWebAuthnUser(ctx, c, userID)
}

func requireWebAuthn(c *Core) (*webauthn.WebAuthn, error) {
	if c.webAuthn == nil || c.passkeys == nil {
		return nil, ErrPasskeysDisabled
	}
	return c.webAuthn, nil
}

func requirePasskeyChallenges(c *Core) error {
	if c.challenges == nil {
		return ErrPasskeysDisabled
	}
	return nil
}

func storePasskeyChallenge(ctx context.Context, c *Core, userID *string, session *webauthn.SessionData, kind PasskeyChallengeKind) (string, error) {
	now := time.Now().UTC()
	if session.Expires.IsZero() {
		session.Expires = now.Add(passkeyChallengeDuration)
	}
	data, err := json.Marshal(session)
	if err != nil {
		return "", err
	}
	if err := c.challenges.DeleteExpiredPasskeyChallenges(ctx, now); err != nil {
		return "", err
	}
	id, err := uuid.NewV4()
	if err != nil {
		return "", err
	}
	challenge := PasskeyChallenge{
		ID: id.String(), UserID: userID, SessionData: data, Kind: kind,
		ExpiresAt: session.Expires.UTC().Truncate(time.Microsecond),
	}
	if err := c.challenges.CreatePasskeyChallenge(ctx, challenge); err != nil {
		return "", err
	}
	return challenge.ID, nil
}

func consumePasskeyChallenge(ctx context.Context, c *Core, id string, kind PasskeyChallengeKind, userID *string) (webauthn.SessionData, error) {
	challenge, err := c.challenges.ConsumePasskeyChallenge(ctx, id)
	if err != nil {
		if errors.Is(err, ErrInvalidPasskeyChallenge) {
			return webauthn.SessionData{}, ErrInvalidPasskeyChallenge
		}
		return webauthn.SessionData{}, err
	}
	if challenge.Kind != kind || !equalOptionalString(challenge.UserID, userID) || !challenge.ExpiresAt.After(time.Now()) {
		return webauthn.SessionData{}, ErrInvalidPasskeyChallenge
	}
	var session webauthn.SessionData
	if err := json.Unmarshal(challenge.SessionData, &session); err != nil {
		return webauthn.SessionData{}, fmt.Errorf("decode passkey challenge: %w", err)
	}
	return session, nil
}

func equalOptionalString(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

type BeginPasskeyRegistrationParams struct{}

type PasskeyRegistrationOptions struct {
	Options     *protocol.CredentialCreation `json:"options"`
	ChallengeID string                       `json:"challenge_id"`
}

var BeginPasskeyRegistration = Define(ActionDef[BeginPasskeyRegistrationParams, PasskeyRegistrationOptions]{
	Name: "begin_passkey_registration", Description: "Begin registering a passkey for the current user.", Mutating: true,
	Handler: func(ctx context.Context, c *Core, _ BeginPasskeyRegistrationParams) (PasskeyRegistrationOptions, error) {
		wan, err := requireWebAuthn(c)
		if err != nil {
			return PasskeyRegistrationOptions{}, err
		}
		if err := requirePasskeyChallenges(c); err != nil {
			return PasskeyRegistrationOptions{}, err
		}
		user, err := currentPasskeyUser(ctx, c)
		if err != nil {
			return PasskeyRegistrationOptions{}, err
		}
		creation, session, err := wan.BeginRegistration(user,
			webauthn.WithResidentKeyRequirement(protocol.ResidentKeyRequirementPreferred),
			webauthn.WithConveyancePreference(protocol.PreferNoAttestation),
			webauthn.WithExclusions(webauthn.Credentials(user.credentials).CredentialDescriptors()),
		)
		if err != nil {
			return PasskeyRegistrationOptions{}, err
		}
		challengeID, err := storePasskeyChallenge(ctx, c, &user.user.ID, session, PasskeyRegistrationChallenge)
		if err != nil {
			return PasskeyRegistrationOptions{}, err
		}
		return PasskeyRegistrationOptions{Options: creation, ChallengeID: challengeID}, nil
	},
})

type FinishPasskeyRegistrationParams struct {
	ChallengeID string          `json:"challenge_id"`
	Credential  json.RawMessage `json:"credential"`
	Description string          `json:"description"`
}

func (p *FinishPasskeyRegistrationParams) Validate() error {
	if e := validID("challenge_id", p.ChallengeID); e != nil {
		return e
	}
	if len(p.Credential) == 0 {
		return errors.New("credential is required")
	}
	p.Description = strings.TrimSpace(p.Description)
	if utf8.RuneCountInString(p.Description) > maxPasskeyDescriptionLength {
		return fmt.Errorf("description must be at most %d characters", maxPasskeyDescriptionLength)
	}
	return nil
}

var FinishPasskeyRegistration = Define(ActionDef[FinishPasskeyRegistrationParams, PasskeyInfo]{
	Name: "finish_passkey_registration", Description: "Verify and save a passkey registration response.", Mutating: true,
	Handler: func(ctx context.Context, c *Core, p FinishPasskeyRegistrationParams) (PasskeyInfo, error) {
		wan, err := requireWebAuthn(c)
		if err != nil {
			return PasskeyInfo{}, err
		}
		if err := requirePasskeyChallenges(c); err != nil {
			return PasskeyInfo{}, err
		}
		user, err := currentPasskeyUser(ctx, c)
		if err != nil {
			return PasskeyInfo{}, err
		}
		session, err := consumePasskeyChallenge(ctx, c, p.ChallengeID, PasskeyRegistrationChallenge, &user.user.ID)
		if err != nil {
			return PasskeyInfo{}, err
		}
		parsed, err := protocol.ParseCredentialCreationResponseBody(bytes.NewReader(p.Credential))
		if err != nil {
			return PasskeyInfo{}, ErrPasskeyVerificationFailed
		}
		credential, err := wan.CreateCredential(user, session, parsed)
		if err != nil {
			return PasskeyInfo{}, ErrPasskeyVerificationFailed
		}
		passkeyID, err := newID()
		if err != nil {
			return PasskeyInfo{}, err
		}
		passkey, err := c.passkeys.CreatePasskey(ctx, Passkey{
			ID: passkeyID, UserID: user.user.ID, CredentialID: credential.ID, PublicKey: credential.PublicKey,
			AAGUID: credential.Authenticator.AAGUID, SignCount: credential.Authenticator.SignCount,
			BackupEligible: credential.Flags.BackupEligible, BackupState: credential.Flags.BackupState,
			Description: p.Description,
		})
		if err != nil {
			return PasskeyInfo{}, err
		}
		return passkeyInfo(passkey), nil
	},
})

type BeginPasskeyLoginParams struct{}

type PasskeyLoginOptions struct {
	Options     *protocol.CredentialAssertion `json:"options"`
	ChallengeID string                        `json:"challenge_id"`
}

var BeginPasskeyLogin = Define(ActionDef[BeginPasskeyLoginParams, PasskeyLoginOptions]{
	Name: "begin_passkey_login", Public: true, Description: "Begin a discoverable passkey login.", Mutating: true,
	Handler: func(ctx context.Context, c *Core, _ BeginPasskeyLoginParams) (PasskeyLoginOptions, error) {
		wan, err := requireWebAuthn(c)
		if err != nil {
			return PasskeyLoginOptions{}, err
		}
		if err := requirePasskeyChallenges(c); err != nil {
			return PasskeyLoginOptions{}, err
		}
		assertion, session, err := wan.BeginDiscoverableLogin()
		if err != nil {
			return PasskeyLoginOptions{}, err
		}
		challengeID, err := storePasskeyChallenge(ctx, c, nil, session, PasskeyLoginChallenge)
		if err != nil {
			return PasskeyLoginOptions{}, err
		}
		return PasskeyLoginOptions{Options: assertion, ChallengeID: challengeID}, nil
	},
})

type FinishPasskeyLoginParams struct {
	ChallengeID string          `json:"challenge_id"`
	Credential  json.RawMessage `json:"credential"`
}

func (p FinishPasskeyLoginParams) Validate() error {
	if e := validID("challenge_id", p.ChallengeID); e != nil {
		return e
	}
	if len(p.Credential) == 0 {
		return errors.New("credential is required")
	}
	return nil
}

var FinishPasskeyLogin = Define(ActionDef[FinishPasskeyLoginParams, AuthSession]{
	Name: "finish_passkey_login", Public: true, Description: "Verify a passkey response and create a session.", Mutating: true,
	Handler: func(ctx context.Context, c *Core, p FinishPasskeyLoginParams) (AuthSession, error) {
		wan, err := requireWebAuthn(c)
		if err != nil {
			return AuthSession{}, err
		}
		if err := requirePasskeyChallenges(c); err != nil {
			return AuthSession{}, err
		}
		sessionData, err := consumePasskeyChallenge(ctx, c, p.ChallengeID, PasskeyLoginChallenge, nil)
		if err != nil {
			return AuthSession{}, err
		}
		parsed, err := protocol.ParseCredentialRequestResponseBody(bytes.NewReader(p.Credential))
		if err != nil {
			return AuthSession{}, ErrPasskeyVerificationFailed
		}
		var resolved *webAuthnUser
		var resolveErr error
		resolve := func(_ []byte, userHandle []byte) (webauthn.User, error) {
			uid, err := uuid.FromBytes(userHandle)
			if err != nil {
				resolveErr = ErrUserNotFound
				return nil, ErrUserNotFound
			}
			user, err := loadWebAuthnUser(ctx, c, uid.String())
			if err != nil {
				resolveErr = err
				return nil, err
			}
			resolved = user
			return user, nil
		}
		_, credential, err := wan.ValidatePasskeyLogin(resolve, sessionData, parsed)
		if err != nil || resolved == nil || credential == nil {
			if resolveErr != nil && !errors.Is(resolveErr, ErrUserNotFound) {
				return AuthSession{}, resolveErr
			}
			return AuthSession{}, ErrPasskeyVerificationFailed
		}
		var session Session
		err = c.tx.InTx(ctx, func(txCtx context.Context) error {
			if err := c.passkeys.UpdatePasskeyCredential(txCtx, resolved.user.ID, credential.ID, credential.Authenticator.SignCount, credential.Flags.BackupState); err != nil {
				return err
			}
			session, err = newSession(resolved.user.ID)
			if err != nil {
				return err
			}
			return c.sessions.CreateSession(txCtx, session)
		})
		if err != nil {
			if errors.Is(err, ErrPasskeyNotFound) {
				return AuthSession{}, ErrPasskeyVerificationFailed
			}
			return AuthSession{}, err
		}
		return authSession(resolved.user, session), nil
	},
})

type ListPasskeysParams struct{}

var ListPasskeys = Define(ActionDef[ListPasskeysParams, []PasskeyInfo]{
	Name: "list_passkeys", Description: "List the current user's passkeys.",
	Handler: func(ctx context.Context, c *Core, _ ListPasskeysParams) ([]PasskeyInfo, error) {
		if _, err := requireWebAuthn(c); err != nil {
			return nil, err
		}
		userID, err := requiredUser(ctx)
		if err != nil {
			return nil, err
		}
		passkeys, err := c.passkeys.ListPasskeysByUser(ctx, userID)
		if err != nil {
			return nil, err
		}
		infos := make([]PasskeyInfo, len(passkeys))
		for i, passkey := range passkeys {
			infos[i] = passkeyInfo(passkey)
		}
		return infos, nil
	},
})

type RenamePasskeyParams struct {
	PasskeyID   string `json:"passkey_id"`
	Description string `json:"description"`
}

func (p *RenamePasskeyParams) Validate() error {
	if e := validID("passkey_id", p.PasskeyID); e != nil {
		return e
	}
	p.Description = strings.TrimSpace(p.Description)
	if utf8.RuneCountInString(p.Description) > maxPasskeyDescriptionLength {
		return fmt.Errorf("description must be at most %d characters", maxPasskeyDescriptionLength)
	}
	return nil
}

var RenamePasskey = Define(ActionDef[RenamePasskeyParams, PasskeyInfo]{
	Name: "rename_passkey", Description: "Rename one of the current user's passkeys.", Mutating: true,
	Handler: func(ctx context.Context, c *Core, p RenamePasskeyParams) (PasskeyInfo, error) {
		if _, err := requireWebAuthn(c); err != nil {
			return PasskeyInfo{}, err
		}
		userID, err := requiredUser(ctx)
		if err != nil {
			return PasskeyInfo{}, err
		}
		passkey, err := c.passkeys.UpdatePasskeyDescription(ctx, userID, p.PasskeyID, p.Description)
		if err != nil {
			return PasskeyInfo{}, err
		}
		return passkeyInfo(passkey), nil
	},
})

type DeletePasskeyParams struct {
	PasskeyID string `json:"passkey_id"`
}

func (p DeletePasskeyParams) Validate() error {
	if e := validID("passkey_id", p.PasskeyID); e != nil {
		return e
	}
	return nil
}

var DeletePasskey = Define(ActionDef[DeletePasskeyParams, struct{}]{
	Name: "delete_passkey", Description: "Delete one of the current user's passkeys.", Mutating: true,
	Handler: func(ctx context.Context, c *Core, p DeletePasskeyParams) (struct{}, error) {
		if _, err := requireWebAuthn(c); err != nil {
			return struct{}{}, err
		}
		userID, err := requiredUser(ctx)
		if err != nil {
			return struct{}{}, err
		}
		return struct{}{}, c.passkeys.DeletePasskey(ctx, userID, p.PasskeyID)
	},
})

func passkeyInfo(p Passkey) PasskeyInfo {
	return PasskeyInfo{ID: p.ID, Description: p.Description, CreatedAt: p.CreatedAt}
}
