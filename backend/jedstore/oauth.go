package jedstore

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/logger4life/backend/core"
)

func (s *Store) CreateOAuthClient(ctx context.Context, client core.OAuthClient) error {
	return s.CreateOAuthClientLimited(ctx, client, core.OAuthDefaultMaxClients)
}

func (s *Store) CreateOAuthClientLimited(ctx context.Context, client core.OAuthClient, limit int) error {
	if limit <= 0 {
		return core.ErrOAuthClientLimit
	}
	return s.InTx(ctx, func(ctx context.Context) error {
		var count int
		if err := s.conn(ctx).QueryRow(ctx, `SELECT count(*) FROM oauth_clients`).Scan(&count); err != nil {
			return err
		}
		if count >= limit {
			return core.ErrOAuthClientLimit
		}
		var clientName *string
		if client.ClientName != "" {
			clientName = &client.ClientName
		}
		_, err := s.conn(ctx).Exec(ctx,
			`INSERT INTO oauth_clients (id, redirect_uris, client_name)
		 VALUES ($1, $2, $3)`,
			client.ID, client.RedirectURIs, clientName,
		)
		return err
	})
}

func (s *Store) PruneUnusedOAuthClients(ctx context.Context, before time.Time) (int64, error) {
	var deleted int64
	err := s.InTx(ctx, func(ctx context.Context) error {
		tag, err := s.conn(ctx).Exec(ctx, `DELETE FROM oauth_clients WHERE id IN (SELECT oc.id FROM oauth_clients oc
		 WHERE oc.created_at < $1
		 AND NOT EXISTS (SELECT 1 FROM oauth_authorization_codes c WHERE c.client_id = oc.id)
		 AND NOT EXISTS (SELECT 1 FROM oauth_token_families f WHERE f.client_id = oc.id)
		 AND NOT EXISTS (SELECT 1 FROM oauth_access_tokens a WHERE a.client_id = oc.id)
		 AND NOT EXISTS (SELECT 1 FROM oauth_refresh_tokens r WHERE r.client_id = oc.id)
		 ORDER BY oc.id LIMIT 1000)`, before)
		deleted = tag.RowsAffected()
		return err
	})
	return deleted, err
}

func (s *Store) GetOAuthClient(ctx context.Context, id string) (core.OAuthClient, error) {
	var client core.OAuthClient
	err := s.conn(ctx).QueryRow(ctx,
		`SELECT id, redirect_uris, COALESCE(client_name, '')
		 FROM oauth_clients WHERE id = $1`, id,
	).Scan(&client.ID, &client.RedirectURIs, &client.ClientName)
	if errors.Is(err, errNoRows) {
		return core.OAuthClient{}, core.ErrOAuthRecordNotFound
	}
	return client, err
}

func (s *Store) CreateAuthorizationCode(ctx context.Context, codeHash []byte, c core.OAuthAuthorizationCode) error {
	_, err := s.conn(ctx).Exec(ctx,
		`INSERT INTO oauth_authorization_codes
		   (code_hash, client_id, user_id, redirect_uri, scope, audience,
		    code_challenge, code_challenge_method, expires_at, authorization_code_only)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		codeHash, c.ClientID, c.UserID, c.RedirectURI, c.Scope, c.Audience,
		c.CodeChallenge, c.CodeChallengeMethod, c.ExpiresAt, c.AuthorizationCodeOnly,
	)
	return err
}

func (s *Store) CreateMetadataAuthorizationCode(ctx context.Context, hash []byte, code core.OAuthAuthorizationCode, limit int) error {
	return s.InTx(ctx, func(ctx context.Context) error {
		// The writer lock serializes identity admission, quota, cleanup,
		// and code insertion. No fetched metadata is stored in oauth_clients.
		if _, err := s.GetOAuthClient(ctx, code.ClientID); errors.Is(err, core.ErrOAuthRecordNotFound) {
			if err := s.CreateOAuthClientLimited(ctx, core.OAuthClient{ID: code.ClientID, RedirectURIs: []string{}}, limit); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		return s.CreateAuthorizationCode(ctx, hash, code)
	})
}

// ConsumeAuthorizationCode atomically finds an unused, unexpired code and
// marks it used, so a second consumption reports ErrOAuthRecordNotFound.
// That single statement is the replay defense.
func (s *Store) ConsumeAuthorizationCode(ctx context.Context, codeHash []byte) (core.OAuthAuthorizationCode, error) {
	var c core.OAuthAuthorizationCode
	err := s.conn(ctx).QueryRow(ctx,
		`UPDATE oauth_authorization_codes
		    SET used = true
		  WHERE code_hash = $1 AND used = false AND expires_at > now()
		  RETURNING client_id, user_id, redirect_uri, scope, audience,
		            code_challenge, code_challenge_method, expires_at, authorization_code_only`,
		codeHash,
	).Scan(&c.ClientID, &c.UserID, &c.RedirectURI, &c.Scope, &c.Audience,
		&c.CodeChallenge, &c.CodeChallengeMethod, &c.ExpiresAt, &c.AuthorizationCodeOnly)
	if errors.Is(err, errNoRows) {
		return core.OAuthAuthorizationCode{}, core.ErrOAuthRecordNotFound
	}
	return c, err
}

// CreateTokenPair inserts the paired refresh and access rows in one
// transaction. The grant's family carries onto both so that detected reuse
// can revoke the entire chain descended from a single authorization.
func (s *Store) CreateTokenPair(ctx context.Context, pair core.OAuthTokenPair) error {
	return s.InTx(ctx, func(ctx context.Context) error {
		conn := s.conn(ctx)
		if _, err := conn.Exec(ctx,
			`INSERT INTO oauth_token_families (id, client_id, user_id)
			 VALUES ($1, $2, $3) ON CONFLICT (id) DO NOTHING`,
			pair.Grant.FamilyID, pair.Grant.ClientID, pair.Grant.UserID,
		); err != nil {
			return err
		}
		revoked, err := s.lockOAuthTokenFamily(ctx, pair.Grant.FamilyID)
		if err != nil {
			return err
		}
		if revoked {
			return core.ErrOAuthRefreshReuse
		}
		if len(pair.RefreshTokenHash) > 0 {
			if _, err := conn.Exec(ctx,
				`INSERT INTO oauth_refresh_tokens
			   (token_hash, client_id, user_id, family_id, scope, audience, expires_at)
			 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
				pair.RefreshTokenHash, pair.Grant.ClientID, pair.Grant.UserID,
				pair.Grant.FamilyID, pair.Grant.Scope, pair.Grant.Audience, pair.RefreshExpiresAt,
			); err != nil {
				return err
			}
		}
		_, err = conn.Exec(ctx,
			`INSERT INTO oauth_access_tokens
			   (token_hash, client_id, user_id, refresh_token_hash, family_id, scope, audience, expires_at)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
			pair.AccessTokenHash, pair.Grant.ClientID, pair.Grant.UserID, pair.RefreshTokenHash,
			pair.Grant.FamilyID, pair.Grant.Scope, pair.Grant.Audience, pair.AccessExpiresAt,
		)
		return err
	})
}

func (s *Store) GetGrantByAccessToken(ctx context.Context, tokenHash []byte) (core.OAuthGrant, error) {
	var g core.OAuthGrant
	err := s.conn(ctx).QueryRow(ctx,
		`SELECT a.client_id, a.user_id, u.username, a.scope, a.audience, a.family_id
		   FROM oauth_access_tokens a
		   JOIN users u ON u.id = a.user_id
		   JOIN oauth_token_families f ON f.id = a.family_id
		  WHERE a.token_hash = $1 AND a.expires_at > now() AND f.revoked = false`,
		tokenHash,
	).Scan(&g.ClientID, &g.UserID, &g.Username, &g.Scope, &g.Audience, &g.FamilyID)
	if errors.Is(err, errNoRows) {
		return core.OAuthGrant{}, core.ErrOAuthRecordNotFound
	}
	return g, err
}

// ConsumeRefreshToken validates a refresh token and, atomically:
//   - missing row → ErrOAuthRecordNotFound
//   - already revoked → ErrOAuthRefreshReuse, after revoking the whole
//     family_id chain (OAuth 2.1 BCP §4.14.2)
//   - expired → ErrOAuthRecordNotFound
//   - otherwise: mark it revoked, delete the access token chained to it, and
//     return the grant so a replacement pair can be issued under the same
//     family.
func (s *Store) ConsumeRefreshToken(ctx context.Context, tokenHash []byte) (core.OAuthGrant, error) {
	var g core.OAuthGrant
	var reuse bool
	err := s.InTx(ctx, func(ctx context.Context) error {
		conn := s.conn(ctx)
		// Lock the family before any token row. Replays of ancestors and
		// issuance of descendants must serialize on the same record.
		var familyID string
		if err := conn.QueryRow(ctx, `SELECT family_id FROM oauth_refresh_tokens WHERE token_hash = $1`, tokenHash).Scan(&familyID); err != nil {
			if errors.Is(err, errNoRows) {
				return core.ErrOAuthRecordNotFound
			}
			return err
		}
		familyRevoked, err := s.lockOAuthTokenFamily(ctx, familyID)
		if err != nil {
			return err
		}
		if familyRevoked {
			reuse = true
			return nil
		}
		var expiresAt time.Time
		var revoked bool
		err = conn.QueryRow(ctx,
			`SELECT client_id, user_id, family_id, scope, audience, expires_at, revoked
			   FROM oauth_refresh_tokens
			  WHERE token_hash = $1`,
			tokenHash,
		).Scan(&g.ClientID, &g.UserID, &g.FamilyID, &g.Scope, &g.Audience, &expiresAt, &revoked)
		if errors.Is(err, errNoRows) {
			return core.ErrOAuthRecordNotFound
		}
		if err != nil {
			return err
		}

		if revoked {
			// Reuse. Revoke every refresh token in the family and drop every
			// access token belonging to them. The revocation must survive, so
			// commit it and report the reuse to the caller afterward.
			if err := s.revokeOAuthTokenFamily(ctx, g.FamilyID); err != nil {
				return err
			}
			reuse = true
			return nil
		}

		if time.Now().After(expiresAt) {
			return core.ErrOAuthRecordNotFound
		}

		if _, err := conn.Exec(ctx,
			`UPDATE oauth_refresh_tokens SET revoked = true WHERE token_hash = $1`,
			tokenHash,
		); err != nil {
			return err
		}
		// Access tokens chained to this refresh token become unusable once
		// the refresh has rotated, per OAuth 2.1 best practice.
		_, err = conn.Exec(ctx,
			`DELETE FROM oauth_access_tokens WHERE refresh_token_hash = $1`,
			tokenHash,
		)
		return err
	})
	if err != nil {
		return core.OAuthGrant{}, err
	}
	if reuse {
		return core.OAuthGrant{}, core.ErrOAuthRefreshReuse
	}
	return g, nil
}

func (s *Store) RevokeAccessToken(ctx context.Context, tokenHash []byte) error {
	_, err := s.conn(ctx).Exec(ctx, `DELETE FROM oauth_access_tokens WHERE token_hash = $1`, tokenHash)
	return err
}

func (s *Store) RevokeRefreshToken(ctx context.Context, tokenHash []byte) error {
	return s.InTx(ctx, func(ctx context.Context) error {
		var familyID string
		if err := s.conn(ctx).QueryRow(ctx, `SELECT family_id FROM oauth_refresh_tokens WHERE token_hash = $1`, tokenHash).Scan(&familyID); err != nil {
			if errors.Is(err, errNoRows) {
				return nil
			}
			return err
		}
		if _, err := s.lockOAuthTokenFamily(ctx, familyID); err != nil {
			return err
		}
		return s.revokeOAuthTokenFamily(ctx, familyID)
	})
}

// lockOAuthTokenFamily must run in the transaction that issues, consumes,
// or revokes tokens. The family record outlives individual token rotations.
// InTx already holds jed's exclusive writer lock, so no FOR UPDATE is needed.
func (s *Store) lockOAuthTokenFamily(ctx context.Context, familyID string) (bool, error) {
	var revoked bool
	err := s.conn(ctx).QueryRow(ctx,
		`SELECT revoked FROM oauth_token_families WHERE id = $1`, familyID,
	).Scan(&revoked)
	if errors.Is(err, errNoRows) {
		return false, core.ErrOAuthRecordNotFound
	}
	return revoked, err
}

// The caller holds the family lock until this revocation is committed.
func (s *Store) revokeOAuthTokenFamily(ctx context.Context, familyID string) error {
	conn := s.conn(ctx)
	if _, err := conn.Exec(ctx, `UPDATE oauth_token_families SET revoked = true WHERE id = $1`, familyID); err != nil {
		return err
	}
	if _, err := conn.Exec(ctx, `UPDATE oauth_refresh_tokens SET revoked = true WHERE family_id = $1`, familyID); err != nil {
		return err
	}
	_, err := conn.Exec(ctx, `DELETE FROM oauth_access_tokens WHERE family_id = $1`, familyID)
	return err
}
