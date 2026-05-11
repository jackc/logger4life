package backend

import (
	"context"
	"crypto/sha256"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// errOAuthNotFound is returned by lookups when a row is missing or already
// invalidated. Callers translate this into the appropriate OAuth error.
var errOAuthNotFound = errors.New("oauth: record not found")

// errOAuthReuseDetected is returned by consumeRefreshToken when a refresh
// token that has already been revoked is presented again. The entire token
// family is revoked as a side effect; callers should log this but return a
// generic invalid_grant to the client so we don't leak the detection.
var errOAuthReuseDetected = errors.New("oauth: refresh token reuse detected")

// hashToken applies SHA-256 to an opaque token. The hash is what we persist
// — the plaintext token never lands in the database.
func hashToken(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}

// ===== Clients =====

type oauthClient struct {
	ID           string
	RedirectURIs []string
	ClientName   string
}

func createOAuthClient(ctx context.Context, pool *pgxpool.Pool, c oauthClient) error {
	_, err := pool.Exec(ctx,
		`INSERT INTO oauth_clients (id, redirect_uris, client_name)
		 VALUES ($1, $2, NULLIF($3, ''))`,
		c.ID, c.RedirectURIs, c.ClientName,
	)
	return err
}

func getOAuthClient(ctx context.Context, pool *pgxpool.Pool, id string) (*oauthClient, error) {
	var c oauthClient
	err := pool.QueryRow(ctx,
		`SELECT id, redirect_uris, COALESCE(client_name, '')
		 FROM oauth_clients WHERE id = $1`,
		id,
	).Scan(&c.ID, &c.RedirectURIs, &c.ClientName)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, errOAuthNotFound
		}
		return nil, err
	}
	return &c, nil
}

// ===== Authorization codes =====

type oauthAuthorizationCode struct {
	ClientID            string
	UserID              string
	Username            string // hydrated from users join
	RedirectURI         string
	Scope               string
	Audience            string
	CodeChallenge       string
	CodeChallengeMethod string
	ExpiresAt           time.Time
}

func createAuthorizationCode(ctx context.Context, pool *pgxpool.Pool, code string, c oauthAuthorizationCode) error {
	_, err := pool.Exec(ctx,
		`INSERT INTO oauth_authorization_codes
		   (code_hash, client_id, user_id, redirect_uri, scope, audience,
		    code_challenge, code_challenge_method, expires_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		hashToken(code), c.ClientID, c.UserID, c.RedirectURI, c.Scope, c.Audience,
		c.CodeChallenge, c.CodeChallengeMethod, c.ExpiresAt,
	)
	return err
}

// consumeAuthorizationCode atomically finds the code, marks it used, and
// returns its row. A second consumption returns errOAuthNotFound — this is
// our replay defense.
func consumeAuthorizationCode(ctx context.Context, pool *pgxpool.Pool, code string) (*oauthAuthorizationCode, error) {
	var c oauthAuthorizationCode
	err := pool.QueryRow(ctx,
		`UPDATE oauth_authorization_codes
		    SET used = true
		  WHERE code_hash = $1 AND used = false AND expires_at > now()
		  RETURNING client_id, user_id, redirect_uri, scope, audience,
		            code_challenge, code_challenge_method, expires_at`,
		hashToken(code),
	).Scan(&c.ClientID, &c.UserID, &c.RedirectURI, &c.Scope, &c.Audience,
		&c.CodeChallenge, &c.CodeChallengeMethod, &c.ExpiresAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, errOAuthNotFound
		}
		return nil, err
	}
	// Hydrate username for downstream session use.
	_ = pool.QueryRow(ctx, `SELECT username FROM users WHERE id = $1`, c.UserID).Scan(&c.Username)
	return &c, nil
}

// ===== Access + refresh tokens =====

type oauthTokens struct {
	AccessToken      string
	RefreshToken     string
	AccessExpiresAt  time.Time
	RefreshExpiresAt time.Time
}

type oauthAccessTokenRow struct {
	ClientID  string
	UserID    string
	Username  string
	Scope     string
	Audience  string
	FamilyID  string
	ExpiresAt time.Time
}

// persistTokenPair inserts a paired access + refresh token row. The family_id
// in info is carried onto both rows so that detected reuse can revoke the
// entire chain originating from a single authorization grant.
func persistTokenPair(ctx context.Context, pool *pgxpool.Pool, t oauthTokens, info oauthAccessTokenRow) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx,
		`INSERT INTO oauth_refresh_tokens
		   (token_hash, client_id, user_id, family_id, scope, audience, expires_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		hashToken(t.RefreshToken), info.ClientID, info.UserID, info.FamilyID,
		info.Scope, info.Audience, t.RefreshExpiresAt,
	); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO oauth_access_tokens
		   (token_hash, client_id, user_id, refresh_token_hash, family_id, scope, audience, expires_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		hashToken(t.AccessToken), info.ClientID, info.UserID, hashToken(t.RefreshToken),
		info.FamilyID, info.Scope, info.Audience, t.AccessExpiresAt,
	); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func lookupAccessToken(ctx context.Context, pool *pgxpool.Pool, token string) (*oauthAccessTokenRow, error) {
	var r oauthAccessTokenRow
	err := pool.QueryRow(ctx,
		`SELECT a.client_id, a.user_id, u.username, a.scope, a.audience, a.expires_at
		   FROM oauth_access_tokens a
		   JOIN users u ON u.id = a.user_id
		  WHERE a.token_hash = $1`,
		hashToken(token),
	).Scan(&r.ClientID, &r.UserID, &r.Username, &r.Scope, &r.Audience, &r.ExpiresAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, errOAuthNotFound
		}
		return nil, err
	}
	if time.Now().After(r.ExpiresAt) {
		return nil, errOAuthNotFound
	}
	return &r, nil
}

// consumeRefreshToken validates a refresh token and, atomically:
//   - if the row is missing → errOAuthNotFound
//   - if the row exists but is already revoked → errOAuthReuseDetected, and
//     the entire family_id chain is revoked as a side effect
//   - if the row exists but is expired → errOAuthNotFound
//   - otherwise: mark revoked=true, delete the chained access token, and
//     return the bound user + client + scope + audience + family_id so the
//     caller can issue a replacement pair under the same family.
func consumeRefreshToken(ctx context.Context, pool *pgxpool.Pool, token string) (*oauthAccessTokenRow, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var r oauthAccessTokenRow
	var revoked bool
	err = tx.QueryRow(ctx,
		`SELECT client_id, user_id, family_id, scope, audience, expires_at, revoked
		   FROM oauth_refresh_tokens
		  WHERE token_hash = $1
		  FOR UPDATE`,
		hashToken(token),
	).Scan(&r.ClientID, &r.UserID, &r.FamilyID, &r.Scope, &r.Audience, &r.ExpiresAt, &revoked)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, errOAuthNotFound
		}
		return nil, err
	}

	if revoked {
		// Refresh-token reuse. Revoke every refresh token in the family and
		// drop every access token belonging to those refresh tokens. This is
		// the OAuth 2.1 BCP §4.14.2 family-revocation response.
		if _, err := tx.Exec(ctx,
			`UPDATE oauth_refresh_tokens SET revoked = true WHERE family_id = $1`,
			r.FamilyID,
		); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx,
			`DELETE FROM oauth_access_tokens WHERE family_id = $1`,
			r.FamilyID,
		); err != nil {
			return nil, err
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return nil, errOAuthReuseDetected
	}

	if time.Now().After(r.ExpiresAt) {
		return nil, errOAuthNotFound
	}

	if _, err := tx.Exec(ctx,
		`UPDATE oauth_refresh_tokens SET revoked = true WHERE token_hash = $1`,
		hashToken(token),
	); err != nil {
		return nil, err
	}
	// Drop any access tokens chained to this refresh token. They become
	// unusable once the refresh has rotated, per OAuth 2.1 best practice.
	if _, err := tx.Exec(ctx,
		`DELETE FROM oauth_access_tokens WHERE refresh_token_hash = $1`,
		hashToken(token),
	); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	_ = pool.QueryRow(ctx, `SELECT username FROM users WHERE id = $1`, r.UserID).Scan(&r.Username)
	return &r, nil
}

func revokeAccessToken(ctx context.Context, pool *pgxpool.Pool, token string) error {
	_, err := pool.Exec(ctx, `DELETE FROM oauth_access_tokens WHERE token_hash = $1`, hashToken(token))
	return err
}

func revokeRefreshToken(ctx context.Context, pool *pgxpool.Pool, token string) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx,
		`UPDATE oauth_refresh_tokens SET revoked = true WHERE token_hash = $1`,
		hashToken(token),
	); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`DELETE FROM oauth_access_tokens WHERE refresh_token_hash = $1`,
		hashToken(token),
	); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
