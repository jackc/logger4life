package dualstore

import (
	"context"

	"github.com/jackc/logger4life/backend/core"
)

func (s *Store) CreateOAuthClient(ctx context.Context, client core.OAuthClient) error {
	return compareError("CreateOAuthClient",
		func() error { return s.primary.CreateOAuthClient(ctx, client) },
		func() error { return s.secondary.CreateOAuthClient(ctx, client) })
}

func (s *Store) GetOAuthClient(ctx context.Context, id string) (core.OAuthClient, error) {
	return compareCall("GetOAuthClient",
		func() (core.OAuthClient, error) { return s.primary.GetOAuthClient(ctx, id) },
		func() (core.OAuthClient, error) { return s.secondary.GetOAuthClient(ctx, id) })
}

func (s *Store) CreateAuthorizationCode(ctx context.Context, codeHash []byte, code core.OAuthAuthorizationCode) error {
	return compareError("CreateAuthorizationCode",
		func() error { return s.primary.CreateAuthorizationCode(ctx, codeHash, code) },
		func() error { return s.secondary.CreateAuthorizationCode(ctx, codeHash, code) })
}

func (s *Store) ConsumeAuthorizationCode(ctx context.Context, codeHash []byte) (core.OAuthAuthorizationCode, error) {
	return compareCall("ConsumeAuthorizationCode",
		func() (core.OAuthAuthorizationCode, error) { return s.primary.ConsumeAuthorizationCode(ctx, codeHash) },
		func() (core.OAuthAuthorizationCode, error) {
			return s.secondary.ConsumeAuthorizationCode(ctx, codeHash)
		})
}

func (s *Store) CreateTokenPair(ctx context.Context, pair core.OAuthTokenPair) error {
	return compareError("CreateTokenPair",
		func() error { return s.primary.CreateTokenPair(ctx, pair) },
		func() error { return s.secondary.CreateTokenPair(ctx, pair) })
}

func (s *Store) GetGrantByAccessToken(ctx context.Context, tokenHash []byte) (core.OAuthGrant, error) {
	return compareCall("GetGrantByAccessToken",
		func() (core.OAuthGrant, error) { return s.primary.GetGrantByAccessToken(ctx, tokenHash) },
		func() (core.OAuthGrant, error) { return s.secondary.GetGrantByAccessToken(ctx, tokenHash) })
}

func (s *Store) ConsumeRefreshToken(ctx context.Context, tokenHash []byte) (core.OAuthGrant, error) {
	return compareCall("ConsumeRefreshToken",
		func() (core.OAuthGrant, error) { return s.primary.ConsumeRefreshToken(ctx, tokenHash) },
		func() (core.OAuthGrant, error) { return s.secondary.ConsumeRefreshToken(ctx, tokenHash) })
}

func (s *Store) RevokeAccessToken(ctx context.Context, tokenHash []byte) error {
	return compareError("RevokeAccessToken",
		func() error { return s.primary.RevokeAccessToken(ctx, tokenHash) },
		func() error { return s.secondary.RevokeAccessToken(ctx, tokenHash) })
}

func (s *Store) RevokeRefreshToken(ctx context.Context, tokenHash []byte) error {
	return compareError("RevokeRefreshToken",
		func() error { return s.primary.RevokeRefreshToken(ctx, tokenHash) },
		func() error { return s.secondary.RevokeRefreshToken(ctx, tokenHash) })
}
