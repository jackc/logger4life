package dualstore

import (
	"context"
	"time"

	"github.com/jackc/logger4life/backend/core"
)

func (s *Store) CreatePasskey(ctx context.Context, passkey core.Passkey) (core.Passkey, error) {
	return compareCall("CreatePasskey",
		func() (core.Passkey, error) { return s.primary.CreatePasskey(ctx, passkey) },
		func() (core.Passkey, error) { return s.secondary.CreatePasskey(ctx, passkey) })
}

func (s *Store) ListPasskeysByUser(ctx context.Context, userID string) ([]core.Passkey, error) {
	return compareCall("ListPasskeysByUser",
		func() ([]core.Passkey, error) { return s.primary.ListPasskeysByUser(ctx, userID) },
		func() ([]core.Passkey, error) { return s.secondary.ListPasskeysByUser(ctx, userID) })
}

func (s *Store) UpdatePasskeyDescription(ctx context.Context, userID, passkeyID, description string) (core.Passkey, error) {
	return compareCall("UpdatePasskeyDescription",
		func() (core.Passkey, error) {
			return s.primary.UpdatePasskeyDescription(ctx, userID, passkeyID, description)
		},
		func() (core.Passkey, error) {
			return s.secondary.UpdatePasskeyDescription(ctx, userID, passkeyID, description)
		})
}

func (s *Store) UpdatePasskeyCredential(ctx context.Context, userID string, credentialID []byte, signCount uint32, backupState bool) error {
	return compareError("UpdatePasskeyCredential",
		func() error {
			return s.primary.UpdatePasskeyCredential(ctx, userID, credentialID, signCount, backupState)
		},
		func() error {
			return s.secondary.UpdatePasskeyCredential(ctx, userID, credentialID, signCount, backupState)
		})
}

func (s *Store) DeletePasskey(ctx context.Context, userID, passkeyID string) error {
	return compareError("DeletePasskey",
		func() error { return s.primary.DeletePasskey(ctx, userID, passkeyID) },
		func() error { return s.secondary.DeletePasskey(ctx, userID, passkeyID) })
}

func (s *Store) CreatePasskeyChallenge(ctx context.Context, challenge core.PasskeyChallenge) error {
	return compareError("CreatePasskeyChallenge",
		func() error { return s.primary.CreatePasskeyChallenge(ctx, challenge) },
		func() error { return s.secondary.CreatePasskeyChallenge(ctx, challenge) })
}

func (s *Store) ConsumePasskeyChallenge(ctx context.Context, id string) (core.PasskeyChallenge, error) {
	return compareCall("ConsumePasskeyChallenge",
		func() (core.PasskeyChallenge, error) { return s.primary.ConsumePasskeyChallenge(ctx, id) },
		func() (core.PasskeyChallenge, error) { return s.secondary.ConsumePasskeyChallenge(ctx, id) })
}

func (s *Store) DeleteExpiredPasskeyChallenges(ctx context.Context, before time.Time) error {
	return compareError("DeleteExpiredPasskeyChallenges",
		func() error { return s.primary.DeleteExpiredPasskeyChallenges(ctx, before) },
		func() error { return s.secondary.DeleteExpiredPasskeyChallenges(ctx, before) })
}
