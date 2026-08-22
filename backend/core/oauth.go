package core

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strings"
	"time"

	"github.com/gofrs/uuid/v5"
	"github.com/jackc/logger4life/backend/domain"
)

// OAuth grant state — clients, authorization codes, and token families — is
// application state that gates access to user data, so it is part of the
// action catalog just like sessions and passkeys. Only protocol translation
// (metadata documents, redirects, the consent page, form parsing) belongs to
// the HTTP adapter.

const (
	OAuthScopeMCP = "mcp"

	OAuthAccessTokenLifespan       = time.Hour
	OAuthRefreshTokenLifespan      = 30 * 24 * time.Hour
	OAuthAuthorizationCodeLifespan = 5 * time.Minute

	oauthAccessTokenPrefix       = "l4l_at_"
	oauthRefreshTokenPrefix      = "l4l_rt_"
	oauthAuthorizationCodePrefix = "l4l_ac_"

	oauthTokenLength = 32
)

// OAuthError is an expected, client-facing OAuth failure. Code is the RFC
// 6749 error code and Description is curated to be safe to hand back to the
// client verbatim; neither ever carries infrastructure detail.
type OAuthError struct {
	// Redirectable reports whether the authorize endpoint may send this
	// failure back to the client's redirect URI. It is false for failures
	// found before the redirect URI is known to belong to the client, which
	// must be rendered inline instead. Token-endpoint errors ignore it.
	Redirectable bool
	Code         string
	Description  string
	cause        error
}

func (e *OAuthError) Error() string { return e.Code + ": " + e.Description }
func (e *OAuthError) Unwrap() error { return e.cause }

// inlineOAuthError builds a failure the authorize endpoint must render itself
// because the request's redirect URI is not yet trusted.
func inlineOAuthError(code, description string) *OAuthError {
	return &OAuthError{Code: code, Description: description}
}

func redirectableOAuthError(code, description string) *OAuthError {
	return &OAuthError{Redirectable: true, Code: code, Description: description}
}

var (
	// ErrOAuthRecordNotFound is what an OAuthStore returns for a missing,
	// expired, or already-invalidated row. Actions translate it into a
	// client-facing OAuthError and never return it directly.
	ErrOAuthRecordNotFound = errors.New("oauth record not found")

	// ErrOAuthRefreshReuse reports that an already-rotated refresh token was
	// presented again. The store revokes the whole token family as a side
	// effect; adapters should log this but answer with the generic
	// invalid_grant so the detection is not leaked to the client.
	ErrOAuthRefreshReuse = errors.New("refresh token reuse detected")

	ErrOAuthInvalidToken          = errors.New("invalid or expired access token")
	ErrOAuthTokenAudienceMismatch = errors.New("access token audience does not match this MCP server")
)

type OAuthClient struct {
	ID           string   `json:"id"`
	RedirectURIs []string `json:"redirect_uris"`
	ClientName   string   `json:"client_name,omitempty"`
}

// OAuthAuthorizationCode is the state bound to an issued authorization code.
// The code itself is never stored — only its hash, supplied separately.
type OAuthAuthorizationCode struct {
	ClientID            string    `json:"client_id"`
	UserID              string    `json:"user_id"`
	RedirectURI         string    `json:"redirect_uri"`
	Scope               string    `json:"scope"`
	Audience            string    `json:"audience"`
	CodeChallenge       string    `json:"code_challenge"`
	CodeChallengeMethod string    `json:"code_challenge_method"`
	ExpiresAt           time.Time `json:"expires_at"`
}

// OAuthGrant is the authorization a token pair was issued under. FamilyID
// ties every rotation of one authorization together so that detected reuse
// can revoke the entire chain. Username is populated only by access-token
// lookups, which need it to build the authenticated user.
type OAuthGrant struct {
	ClientID string `json:"client_id"`
	UserID   string `json:"user_id"`
	Username string `json:"username,omitempty"`
	Scope    string `json:"scope"`
	Audience string `json:"audience"`
	FamilyID string `json:"family_id"`
}

// OAuthTokenPair is the persisted form of an issued access + refresh pair.
// Only hashes cross the store boundary.
type OAuthTokenPair struct {
	Grant            OAuthGrant
	AccessTokenHash  []byte
	RefreshTokenHash []byte
	AccessExpiresAt  time.Time
	RefreshExpiresAt time.Time
}

// OAuthTokens is the plaintext pair handed to the client exactly once.
type OAuthTokens struct {
	AccessToken      string    `json:"access_token"`
	RefreshToken     string    `json:"refresh_token"`
	Scope            string    `json:"scope"`
	AccessExpiresAt  time.Time `json:"access_expires_at"`
	RefreshExpiresAt time.Time `json:"refresh_expires_at"`
}

// OAuthStore is the driven persistence port for OAuth clients, authorization
// codes, and token families. Implementations must consume codes and refresh
// tokens atomically so a replay cannot succeed, and must report a missing or
// invalidated row as ErrOAuthRecordNotFound.
type OAuthStore interface {
	CreateOAuthClient(context.Context, OAuthClient) error
	GetOAuthClient(context.Context, string) (OAuthClient, error)
	CreateAuthorizationCode(context.Context, []byte, OAuthAuthorizationCode) error
	ConsumeAuthorizationCode(context.Context, []byte) (OAuthAuthorizationCode, error)
	CreateTokenPair(context.Context, OAuthTokenPair) error
	GetGrantByAccessToken(context.Context, []byte) (OAuthGrant, error)
	ConsumeRefreshToken(context.Context, []byte) (OAuthGrant, error)
	RevokeAccessToken(context.Context, []byte) error
	RevokeRefreshToken(context.Context, []byte) error
}

// newOAuthToken returns 32 bytes of CSPRNG output, base64url-encoded and
// prefixed for human readability.
func newOAuthToken(prefix string) (string, error) {
	b := make([]byte, oauthTokenLength)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return prefix + base64.RawURLEncoding.EncodeToString(b), nil
}

type RegisterOAuthClientParams struct {
	RedirectURIs []string `json:"redirect_uris"`
	ClientName   string   `json:"client_name,omitempty"`
}

var RegisterOAuthClient = Define(ActionDef[RegisterOAuthClientParams, OAuthClient]{
	Name: "register_oauth_client", Public: true, Description: "Register an OAuth client (RFC 7591 dynamic client registration).", Mutating: true,
	Handler: func(ctx context.Context, c *Core, p RegisterOAuthClientParams) (OAuthClient, error) {
		if len(p.RedirectURIs) == 0 {
			return OAuthClient{}, inlineOAuthError("invalid_redirect_uri", "redirect_uris is required")
		}
		for _, u := range p.RedirectURIs {
			if !domain.ValidRedirectURI(u) {
				return OAuthClient{}, inlineOAuthError("invalid_redirect_uri", "redirect_uri must be https or http://localhost")
			}
		}
		id, err := uuid.NewV7()
		if err != nil {
			return OAuthClient{}, err
		}
		client := OAuthClient{ID: id.String(), RedirectURIs: p.RedirectURIs, ClientName: p.ClientName}
		if err := c.oauth.CreateOAuthClient(ctx, client); err != nil {
			return OAuthClient{}, err
		}
		return client, nil
	},
})

// OAuthAuthorizationParams are the /authorize request parameters, used both
// to validate a pending request and to issue a code once the user approves.
type OAuthAuthorizationParams struct {
	ResponseType        string `json:"response_type"`
	ClientID            string `json:"client_id"`
	RedirectURI         string `json:"redirect_uri"`
	Scope               string `json:"scope"`
	State               string `json:"state"`
	CodeChallenge       string `json:"code_challenge"`
	CodeChallengeMethod string `json:"code_challenge_method"`
	Resource            string `json:"resource"`
}

// OAuthAuthorizationRequest is a validated authorization request: the client
// asking for consent plus the normalized scope and audience a code would
// carry.
type OAuthAuthorizationRequest struct {
	Client   OAuthClient `json:"client"`
	Scope    string      `json:"scope"`
	Audience string      `json:"audience"`
}

// validateAuthorization applies every OAuth 2.1 rule an authorization request
// must satisfy. Both authorization actions run it, so approving a request is
// checked as thoroughly as previewing one.
func (c *Core) validateAuthorization(ctx context.Context, p OAuthAuthorizationParams) (OAuthAuthorizationRequest, error) {
	client, err := c.oauth.GetOAuthClient(ctx, p.ClientID)
	if errors.Is(err, ErrOAuthRecordNotFound) {
		return OAuthAuthorizationRequest{}, inlineOAuthError("invalid_client", "unknown client_id")
	}
	if err != nil {
		return OAuthAuthorizationRequest{}, err
	}
	// Until the redirect URI is known to be registered to this client we
	// cannot safely redirect anything to it, including errors.
	if !domain.RedirectURIRegistered(client.RedirectURIs, p.RedirectURI) {
		return OAuthAuthorizationRequest{}, inlineOAuthError("invalid_redirect_uri", "redirect_uri does not match a registered URI")
	}
	if p.ResponseType != "code" {
		return OAuthAuthorizationRequest{}, redirectableOAuthError("unsupported_response_type", "only response_type=code is supported")
	}
	if p.CodeChallenge == "" || p.CodeChallengeMethod != "S256" {
		return OAuthAuthorizationRequest{}, redirectableOAuthError("invalid_request", "PKCE S256 code_challenge is required")
	}
	if len(p.State) < 8 {
		return OAuthAuthorizationRequest{}, redirectableOAuthError("invalid_request", "state is required and must be at least 8 characters")
	}
	scope := p.Scope
	if scope == "" {
		scope = OAuthScopeMCP
	}
	for _, s := range strings.Fields(scope) {
		if s != OAuthScopeMCP {
			return OAuthAuthorizationRequest{}, redirectableOAuthError("invalid_scope", "unsupported scope "+s)
		}
	}
	// RFC 8707 audience binding: the resource MUST identify this server.
	audience := p.Resource
	if audience == "" {
		audience = c.oauthIssuer
	} else if !domain.SameCanonicalURL(audience, c.oauthIssuer) {
		return OAuthAuthorizationRequest{}, redirectableOAuthError("invalid_target", "resource parameter does not match this server")
	}
	return OAuthAuthorizationRequest{Client: client, Scope: scope, Audience: audience}, nil
}

var PrepareOAuthAuthorization = Define(ActionDef[OAuthAuthorizationParams, OAuthAuthorizationRequest]{
	Name: "prepare_oauth_authorization", Public: true, Description: "Validate an OAuth authorization request and describe the client seeking consent.",
	Handler: func(ctx context.Context, c *Core, p OAuthAuthorizationParams) (OAuthAuthorizationRequest, error) {
		return c.validateAuthorization(ctx, p)
	},
})

type OAuthAuthorizationCodeResult struct {
	Code string `json:"code"`
}

var CreateOAuthAuthorizationCode = Define(ActionDef[OAuthAuthorizationParams, OAuthAuthorizationCodeResult]{
	Name: "create_oauth_authorization_code", Description: "Issue an authorization code for the current user after they approve the request.", Mutating: true,
	Handler: func(ctx context.Context, c *Core, p OAuthAuthorizationParams) (OAuthAuthorizationCodeResult, error) {
		userID, err := requiredUser(ctx)
		if err != nil {
			return OAuthAuthorizationCodeResult{}, err
		}
		req, err := c.validateAuthorization(ctx, p)
		if err != nil {
			return OAuthAuthorizationCodeResult{}, err
		}
		code, err := newOAuthToken(oauthAuthorizationCodePrefix)
		if err != nil {
			return OAuthAuthorizationCodeResult{}, err
		}
		record := OAuthAuthorizationCode{
			ClientID:            req.Client.ID,
			UserID:              userID,
			RedirectURI:         p.RedirectURI,
			Scope:               req.Scope,
			Audience:            req.Audience,
			CodeChallenge:       p.CodeChallenge,
			CodeChallengeMethod: p.CodeChallengeMethod,
			ExpiresAt:           time.Now().Add(OAuthAuthorizationCodeLifespan),
		}
		if err := c.oauth.CreateAuthorizationCode(ctx, domain.HashToken(code), record); err != nil {
			return OAuthAuthorizationCodeResult{}, err
		}
		return OAuthAuthorizationCodeResult{Code: code}, nil
	},
})

// issueTokenPair mints and persists a fresh access + refresh pair under an
// existing grant. The family carries over so a later reuse detection can
// revoke every token descended from the original authorization.
func (c *Core) issueTokenPair(ctx context.Context, grant OAuthGrant) (OAuthTokens, error) {
	access, err := newOAuthToken(oauthAccessTokenPrefix)
	if err != nil {
		return OAuthTokens{}, err
	}
	refresh, err := newOAuthToken(oauthRefreshTokenPrefix)
	if err != nil {
		return OAuthTokens{}, err
	}
	now := time.Now()
	pair := OAuthTokenPair{
		Grant:            grant,
		AccessTokenHash:  domain.HashToken(access),
		RefreshTokenHash: domain.HashToken(refresh),
		AccessExpiresAt:  now.Add(OAuthAccessTokenLifespan),
		RefreshExpiresAt: now.Add(OAuthRefreshTokenLifespan),
	}
	if err := c.oauth.CreateTokenPair(ctx, pair); err != nil {
		return OAuthTokens{}, err
	}
	return OAuthTokens{
		AccessToken:      access,
		RefreshToken:     refresh,
		Scope:            grant.Scope,
		AccessExpiresAt:  pair.AccessExpiresAt,
		RefreshExpiresAt: pair.RefreshExpiresAt,
	}, nil
}

type ExchangeOAuthCodeParams struct {
	ClientID     string `json:"client_id"`
	Code         string `json:"code"`
	RedirectURI  string `json:"redirect_uri"`
	CodeVerifier string `json:"code_verifier"`
	Resource     string `json:"resource"`
}

var ExchangeOAuthCode = Define(ActionDef[ExchangeOAuthCodeParams, OAuthTokens]{
	Name: "exchange_oauth_code", Public: true, Description: "Exchange an authorization code plus PKCE verifier for a token pair.", Mutating: true,
	Handler: func(ctx context.Context, c *Core, p ExchangeOAuthCodeParams) (OAuthTokens, error) {
		if p.ClientID == "" || p.Code == "" || p.CodeVerifier == "" {
			return OAuthTokens{}, inlineOAuthError("invalid_request", "client_id, code, and code_verifier are required")
		}
		// Consuming is atomic: a replay of the same code finds nothing.
		stored, err := c.oauth.ConsumeAuthorizationCode(ctx, domain.HashToken(p.Code))
		if err != nil {
			if errors.Is(err, ErrOAuthRecordNotFound) {
				// Unknown, expired, and already-used codes are deliberately
				// indistinguishable to the client.
				return OAuthTokens{}, inlineOAuthError("invalid_grant", "code is invalid, expired, or already used")
			}
			return OAuthTokens{}, err
		}
		if stored.ClientID != p.ClientID {
			return OAuthTokens{}, inlineOAuthError("invalid_grant", "code was issued to a different client")
		}
		if stored.RedirectURI != p.RedirectURI {
			return OAuthTokens{}, inlineOAuthError("invalid_grant", "redirect_uri does not match the original request")
		}
		if !domain.VerifyPKCE(stored.CodeChallenge, stored.CodeChallengeMethod, p.CodeVerifier) {
			return OAuthTokens{}, inlineOAuthError("invalid_grant", "PKCE verification failed")
		}
		if p.Resource != "" && !domain.SameCanonicalURL(p.Resource, stored.Audience) {
			return OAuthTokens{}, inlineOAuthError("invalid_target", "resource parameter does not match the original request")
		}
		familyID, err := uuid.NewV7()
		if err != nil {
			return OAuthTokens{}, err
		}
		return c.issueTokenPair(ctx, OAuthGrant{
			ClientID: stored.ClientID,
			UserID:   stored.UserID,
			Scope:    stored.Scope,
			Audience: stored.Audience,
			FamilyID: familyID.String(),
		})
	},
})

type RefreshOAuthTokenParams struct {
	ClientID     string `json:"client_id"`
	RefreshToken string `json:"refresh_token"`
	Resource     string `json:"resource"`
}

var RefreshOAuthToken = Define(ActionDef[RefreshOAuthTokenParams, OAuthTokens]{
	Name: "refresh_oauth_token", Public: true, Description: "Rotate a refresh token into a new token pair.", Mutating: true,
	Handler: func(ctx context.Context, c *Core, p RefreshOAuthTokenParams) (OAuthTokens, error) {
		if p.ClientID == "" || p.RefreshToken == "" {
			return OAuthTokens{}, inlineOAuthError("invalid_request", "client_id and refresh_token are required")
		}
		grant, err := c.oauth.ConsumeRefreshToken(ctx, domain.HashToken(p.RefreshToken))
		if err != nil {
			if errors.Is(err, ErrOAuthRecordNotFound) || errors.Is(err, ErrOAuthRefreshReuse) {
				// Reuse keeps its cause so adapters can log the detection,
				// but the client sees the same answer either way.
				return OAuthTokens{}, &OAuthError{
					Code:        "invalid_grant",
					Description: "refresh_token is invalid, expired, or revoked",
					cause:       err,
				}
			}
			return OAuthTokens{}, err
		}
		if grant.ClientID != p.ClientID {
			return OAuthTokens{}, inlineOAuthError("invalid_grant", "refresh_token was issued to a different client")
		}
		if p.Resource != "" && !domain.SameCanonicalURL(p.Resource, grant.Audience) {
			return OAuthTokens{}, inlineOAuthError("invalid_target", "resource parameter does not match the original request")
		}
		return c.issueTokenPair(ctx, grant)
	},
})

type RevokeOAuthTokenParams struct {
	Token         string `json:"token"`
	TokenTypeHint string `json:"token_type_hint,omitempty"`
}

var RevokeOAuthToken = Define(ActionDef[RevokeOAuthTokenParams, struct{}]{
	Name: "revoke_oauth_token", Public: true, Description: "Revoke an access or refresh token (RFC 7009).", Mutating: true,
	Handler: func(ctx context.Context, c *Core, p RevokeOAuthTokenParams) (struct{}, error) {
		if p.Token == "" {
			return struct{}{}, nil
		}
		hash := domain.HashToken(p.Token)
		// The token type is only a hint, so try both stores either way and
		// start with the hinted one.
		revoke := []func(context.Context, []byte) error{c.oauth.RevokeAccessToken, c.oauth.RevokeRefreshToken}
		if p.TokenTypeHint == "refresh_token" {
			revoke[0], revoke[1] = revoke[1], revoke[0]
		}
		var firstErr error
		for _, fn := range revoke {
			if err := fn(ctx, hash); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		return struct{}{}, firstErr
	},
})

type AuthenticateOAuthTokenParams struct {
	Token string `json:"token"`
}

var AuthenticateOAuthToken = Define(ActionDef[AuthenticateOAuthTokenParams, User]{
	Name: "authenticate_oauth_token", Public: true, Description: "Resolve an OAuth access token to its user.",
	Handler: func(ctx context.Context, c *Core, p AuthenticateOAuthTokenParams) (User, error) {
		if p.Token == "" {
			return User{}, ErrOAuthInvalidToken
		}
		grant, err := c.oauth.GetGrantByAccessToken(ctx, domain.HashToken(p.Token))
		if errors.Is(err, ErrOAuthRecordNotFound) {
			return User{}, ErrOAuthInvalidToken
		}
		if err != nil {
			return User{}, err
		}
		if !domain.SameCanonicalURL(grant.Audience, c.oauthIssuer) {
			return User{}, ErrOAuthTokenAudienceMismatch
		}
		return User{ID: grant.UserID, Username: grant.Username}, nil
	},
})
