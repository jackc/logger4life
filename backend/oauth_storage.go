package backend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/ory/fosite"
)

// MCPSession is the per-request session payload persisted alongside each
// fosite Requester. It embeds DefaultSession (for token-lifespan tracking)
// and adds the Logger4Life user identity that authorized the grant.
type MCPSession struct {
	*fosite.DefaultSession
	UserID   string `json:"user_id"`
	Username string `json:"username"`
}

func newMCPSession() *MCPSession {
	return &MCPSession{DefaultSession: &fosite.DefaultSession{}}
}

// Clone is required by fosite.Session. Failing to deep-copy here causes
// subtle aliasing bugs across requests because fosite holds onto sessions.
func (s *MCPSession) Clone() fosite.Session {
	if s == nil {
		return nil
	}
	dup := &MCPSession{UserID: s.UserID, Username: s.Username}
	if s.DefaultSession != nil {
		dup.DefaultSession = s.DefaultSession.Clone().(*fosite.DefaultSession)
	} else {
		dup.DefaultSession = &fosite.DefaultSession{}
	}
	return dup
}

// oauthStorage is the pgx-backed implementation of every fosite storage
// interface our OAuth2 handlers care about (ClientManager, AuthorizeCode,
// AccessToken, RefreshToken, TokenRevocation, PKCE).
type oauthStorage struct {
	pool *pgxpool.Pool
}

func newOAuthStorage(pool *pgxpool.Pool) *oauthStorage {
	return &oauthStorage{pool: pool}
}

// ===== Client management =====

// GetClient implements fosite.ClientManager.
func (s *oauthStorage) GetClient(ctx context.Context, id string) (fosite.Client, error) {
	var c fosite.DefaultClient
	var redirectURIs, grantTypes, responseTypes, scopes, audiences []string
	err := s.pool.QueryRow(ctx,
		`SELECT id, redirect_uris, grant_types, response_types, scopes, audiences
		 FROM oauth_clients WHERE id = $1`,
		id,
	).Scan(&c.ID, &redirectURIs, &grantTypes, &responseTypes, &scopes, &audiences)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fosite.ErrNotFound
		}
		return nil, err
	}
	c.RedirectURIs = redirectURIs
	c.GrantTypes = grantTypes
	c.ResponseTypes = responseTypes
	c.Scopes = scopes
	c.Audience = audiences
	// All clients we register via DCR are public (no client secret), per the
	// MCP authorization spec which targets PKCE-based public clients.
	c.Public = true
	return &c, nil
}

// ClientAssertionJWTValid + SetClientAssertionJWT are required by
// ClientManager but only matter for client_secret_jwt / private_key_jwt
// authentication, neither of which we support. Treat every JTI as unseen.
func (s *oauthStorage) ClientAssertionJWTValid(_ context.Context, _ string) error {
	return nil
}

func (s *oauthStorage) SetClientAssertionJWT(_ context.Context, _ string, _ time.Time) error {
	return nil
}

// createClient is the non-fosite helper used by the DCR endpoint. The
// token_endpoint_auth_method column is always 'none' because we only
// register public PKCE clients.
func (s *oauthStorage) createClient(ctx context.Context, c *fosite.DefaultClient, name, clientURI, logoURI string) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO oauth_clients
		   (id, redirect_uris, grant_types, response_types, scopes, audiences,
		    token_endpoint_auth_method, client_name, client_uri, logo_uri)
		 VALUES ($1, $2, $3, $4, $5, $6, 'none', NULLIF($7,''), NULLIF($8,''), NULLIF($9,''))`,
		c.ID, c.RedirectURIs, c.GrantTypes, c.ResponseTypes, c.Scopes, c.Audience,
		name, clientURI, logoURI,
	)
	return err
}

// ===== Generic session storage =====
//
// All four session tables (authorize codes, access tokens, refresh tokens,
// PKCE) share the same shape, so we route through helpers parameterized by
// table name.

// putSession inserts a fosite Requester into the named table. The signature
// is the storage key; for refresh tokens the access_token signature is
// captured in the session JSON for later RotateRefreshToken lookups.
func (s *oauthStorage) putSession(ctx context.Context, table, signature string, req fosite.Requester, tokenType fosite.TokenType) error {
	sess, _ := req.GetSession().(*MCPSession)
	if sess == nil {
		return fmt.Errorf("oauth_storage: session is not *MCPSession (got %T)", req.GetSession())
	}

	sessionJSON, err := json.Marshal(sess)
	if err != nil {
		return fmt.Errorf("marshal session: %w", err)
	}
	formJSON, err := json.Marshal(req.GetRequestForm())
	if err != nil {
		return fmt.Errorf("marshal form: %w", err)
	}

	expiresAt := sess.GetExpiresAt(tokenType)
	if expiresAt.IsZero() {
		// Defensive default: 1 hour. Fosite's lifespan config should always
		// have populated this, but if it didn't, we'd rather over-expire
		// than persist a never-expiring row.
		expiresAt = time.Now().Add(time.Hour)
	}

	_, err = s.pool.Exec(ctx,
		`INSERT INTO `+table+`
		   (signature, request_id, client_id, user_id, requested_at, expires_at,
		    requested_scopes, granted_scopes, requested_audience, granted_audience,
		    form, session, active)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, true)`,
		signature, req.GetID(), req.GetClient().GetID(), sess.UserID,
		req.GetRequestedAt(), expiresAt,
		nilSafe(req.GetRequestedScopes()), nilSafe(req.GetGrantedScopes()),
		nilSafe(req.GetRequestedAudience()), nilSafe(req.GetGrantedAudience()),
		formJSON, sessionJSON,
	)
	return err
}

// nilSafe converts a nil fosite.Arguments slice to an empty []string so pgx
// inserts an empty array instead of NULL (the columns are NOT NULL).
func nilSafe(a fosite.Arguments) []string {
	if a == nil {
		return []string{}
	}
	out := make([]string, len(a))
	copy(out, []string(a))
	return out
}

// getSession reconstructs a fosite Requester from the named table. The
// `inactiveErr` parameter is returned when the row exists but is marked
// inactive — fosite distinguishes "never existed" (ErrNotFound) from
// "used/revoked" (ErrInvalidatedAuthorizeCode, ErrInactiveToken).
func (s *oauthStorage) getSession(ctx context.Context, table, signature string, _ fosite.Session, inactiveErr error) (fosite.Requester, error) {
	var (
		requestID, clientID, userID                                       string
		requestedAt, expiresAt                                            time.Time
		requestedScopes, grantedScopes, requestedAudience, grantedAudience []string
		formJSON, sessionJSON                                              []byte
		active                                                             bool
	)
	err := s.pool.QueryRow(ctx,
		`SELECT request_id, client_id, user_id, requested_at, expires_at,
		        requested_scopes, granted_scopes, requested_audience, granted_audience,
		        form, session, active
		   FROM `+table+` WHERE signature = $1`,
		signature,
	).Scan(&requestID, &clientID, &userID, &requestedAt, &expiresAt,
		&requestedScopes, &grantedScopes, &requestedAudience, &grantedAudience,
		&formJSON, &sessionJSON, &active)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fosite.ErrNotFound
		}
		return nil, err
	}
	_ = userID // already encoded inside the session JSON; kept for FK + indexing only.

	client, err := s.GetClient(ctx, clientID)
	if err != nil {
		return nil, err
	}

	form := url.Values{}
	if len(formJSON) > 0 {
		if err := json.Unmarshal(formJSON, &form); err != nil {
			return nil, fmt.Errorf("unmarshal form: %w", err)
		}
	}

	sess := newMCPSession()
	if len(sessionJSON) > 0 {
		if err := json.Unmarshal(sessionJSON, sess); err != nil {
			return nil, fmt.Errorf("unmarshal session: %w", err)
		}
	}
	if sess.DefaultSession == nil {
		sess.DefaultSession = &fosite.DefaultSession{}
	}

	req := &fosite.Request{
		ID:                requestID,
		RequestedAt:       requestedAt,
		Client:            client,
		RequestedScope:    fosite.Arguments(requestedScopes),
		GrantedScope:      fosite.Arguments(grantedScopes),
		RequestedAudience: fosite.Arguments(requestedAudience),
		GrantedAudience:   fosite.Arguments(grantedAudience),
		Form:              form,
		Session:           sess,
	}

	if !active && inactiveErr != nil {
		return req, inactiveErr
	}
	return req, nil
}

func (s *oauthStorage) deactivateBySignature(ctx context.Context, table, signature string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE `+table+` SET active = false WHERE signature = $1`, signature)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fosite.ErrNotFound
	}
	return nil
}

func (s *oauthStorage) deleteBySignature(ctx context.Context, table, signature string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM `+table+` WHERE signature = $1`, signature)
	return err
}

func (s *oauthStorage) deactivateByRequestID(ctx context.Context, table, requestID string) error {
	_, err := s.pool.Exec(ctx, `UPDATE `+table+` SET active = false WHERE request_id = $1`, requestID)
	return err
}

func (s *oauthStorage) deleteByRequestID(ctx context.Context, table, requestID string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM `+table+` WHERE request_id = $1`, requestID)
	return err
}

// ===== Authorize codes =====

func (s *oauthStorage) CreateAuthorizeCodeSession(ctx context.Context, code string, req fosite.Requester) error {
	return s.putSession(ctx, "oauth_authorize_codes", code, req, fosite.AuthorizeCode)
}

func (s *oauthStorage) GetAuthorizeCodeSession(ctx context.Context, code string, sess fosite.Session) (fosite.Requester, error) {
	return s.getSession(ctx, "oauth_authorize_codes", code, sess, fosite.ErrInvalidatedAuthorizeCode)
}

func (s *oauthStorage) InvalidateAuthorizeCodeSession(ctx context.Context, code string) error {
	return s.deactivateBySignature(ctx, "oauth_authorize_codes", code)
}

// ===== Access tokens =====

func (s *oauthStorage) CreateAccessTokenSession(ctx context.Context, signature string, req fosite.Requester) error {
	return s.putSession(ctx, "oauth_access_tokens", signature, req, fosite.AccessToken)
}

func (s *oauthStorage) GetAccessTokenSession(ctx context.Context, signature string, sess fosite.Session) (fosite.Requester, error) {
	return s.getSession(ctx, "oauth_access_tokens", signature, sess, fosite.ErrInactiveToken)
}

func (s *oauthStorage) DeleteAccessTokenSession(ctx context.Context, signature string) error {
	return s.deleteBySignature(ctx, "oauth_access_tokens", signature)
}

func (s *oauthStorage) RevokeAccessToken(ctx context.Context, requestID string) error {
	return s.deleteByRequestID(ctx, "oauth_access_tokens", requestID)
}

// ===== Refresh tokens =====

func (s *oauthStorage) CreateRefreshTokenSession(ctx context.Context, signature, _ string, req fosite.Requester) error {
	// We ignore the access-token signature parameter; fosite uses it to chain
	// rotation, but our RotateRefreshToken implementation works by
	// request_id which is sufficient for our flow.
	return s.putSession(ctx, "oauth_refresh_tokens", signature, req, fosite.RefreshToken)
}

func (s *oauthStorage) GetRefreshTokenSession(ctx context.Context, signature string, sess fosite.Session) (fosite.Requester, error) {
	return s.getSession(ctx, "oauth_refresh_tokens", signature, sess, fosite.ErrInactiveToken)
}

func (s *oauthStorage) DeleteRefreshTokenSession(ctx context.Context, signature string) error {
	return s.deleteBySignature(ctx, "oauth_refresh_tokens", signature)
}

func (s *oauthStorage) RevokeRefreshToken(ctx context.Context, requestID string) error {
	return s.deactivateByRequestID(ctx, "oauth_refresh_tokens", requestID)
}

// RotateRefreshToken is called when a refresh-token grant succeeds. We mark
// the prior refresh token (by request_id) inactive so a replay of the old
// one is detected, while leaving the row in place for audit.
func (s *oauthStorage) RotateRefreshToken(ctx context.Context, requestID, _ string) error {
	return s.deactivateByRequestID(ctx, "oauth_refresh_tokens", requestID)
}

// ===== PKCE =====

func (s *oauthStorage) CreatePKCERequestSession(ctx context.Context, signature string, req fosite.Requester) error {
	return s.putSession(ctx, "oauth_pkce_sessions", signature, req, fosite.AuthorizeCode)
}

func (s *oauthStorage) GetPKCERequestSession(ctx context.Context, signature string, sess fosite.Session) (fosite.Requester, error) {
	return s.getSession(ctx, "oauth_pkce_sessions", signature, sess, nil)
}

func (s *oauthStorage) DeletePKCERequestSession(ctx context.Context, signature string) error {
	return s.deleteBySignature(ctx, "oauth_pkce_sessions", signature)
}
