package jedstore

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/logger4life/backend/core"
)

func scanPasskey(row rowScanner) (core.Passkey, error) {
	var passkey core.Passkey
	var signCount int64
	err := row.Scan(
		&passkey.ID, &passkey.UserID, &passkey.CredentialID, &passkey.PublicKey,
		&passkey.AAGUID, &signCount, &passkey.BackupEligible, &passkey.BackupState,
		&passkey.Description, &passkey.CreatedAt,
	)
	if err != nil {
		return core.Passkey{}, err
	}
	passkey.SignCount = uint32(signCount)
	return passkey, nil
}

func translatePasskeyWriteError(err error) error {
	if uniqueViolation(err, "credential_id") {
		return core.ErrPasskeyAlreadyRegistered
	}
	return err
}

func (s *Store) CreatePasskey(ctx context.Context, passkey core.Passkey) (core.Passkey, error) {
	created, err := scanPasskey(s.conn(ctx).QueryRow(ctx,
		`INSERT INTO passkeys
		 (id, user_id, credential_id, public_key, aaguid, sign_count, backup_eligible, backup_state, description)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		 RETURNING id, user_id, credential_id, public_key, aaguid, sign_count,
		           backup_eligible, backup_state, description, created_at`,
		passkey.ID, passkey.UserID, passkey.CredentialID, passkey.PublicKey, passkey.AAGUID,
		passkey.SignCount, passkey.BackupEligible, passkey.BackupState, passkey.Description,
	))
	return created, translatePasskeyWriteError(err)
}

func (s *Store) ListPasskeysByUser(ctx context.Context, userID string) ([]core.Passkey, error) {
	rows, err := s.conn(ctx).Query(ctx,
		`SELECT id, user_id, credential_id, public_key, aaguid, sign_count,
		        backup_eligible, backup_state, description, created_at
		 FROM passkeys WHERE user_id = $1 ORDER BY created_at, id`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	passkeys := []core.Passkey{}
	for rows.Next() {
		passkey, err := scanPasskey(rows)
		if err != nil {
			return nil, err
		}
		passkeys = append(passkeys, passkey)
	}
	return passkeys, rows.Err()
}

func (s *Store) UpdatePasskeyDescription(ctx context.Context, userID, passkeyID, description string) (core.Passkey, error) {
	passkey, err := scanPasskey(s.conn(ctx).QueryRow(ctx,
		`UPDATE passkeys SET description = $1 WHERE id = $2 AND user_id = $3
		 RETURNING id, user_id, credential_id, public_key, aaguid, sign_count,
		           backup_eligible, backup_state, description, created_at`,
		description, passkeyID, userID,
	))
	if errors.Is(err, errNoRows) {
		return core.Passkey{}, core.ErrPasskeyNotFound
	}
	return passkey, err
}

func (s *Store) UpdatePasskeyCredential(ctx context.Context, userID string, credentialID []byte, signCount uint32, backupState bool) error {
	result, err := s.conn(ctx).Exec(ctx,
		`UPDATE passkeys SET sign_count = $1, backup_state = $2
		 WHERE user_id = $3 AND credential_id = $4`,
		signCount, backupState, userID, credentialID,
	)
	if err == nil && result.RowsAffected() == 0 {
		return core.ErrPasskeyNotFound
	}
	return err
}

func (s *Store) DeletePasskey(ctx context.Context, userID, passkeyID string) error {
	result, err := s.conn(ctx).Exec(ctx,
		`DELETE FROM passkeys WHERE id = $1 AND user_id = $2`,
		passkeyID, userID,
	)
	if err == nil && result.RowsAffected() == 0 {
		return core.ErrPasskeyNotFound
	}
	return err
}

func (s *Store) CreatePasskeyChallenge(ctx context.Context, challenge core.PasskeyChallenge) error {
	_, err := s.conn(ctx).Exec(ctx,
		`INSERT INTO webauthn_challenges (id, user_id, session_data, type, expires_at)
		 VALUES ($1, $2, $3, $4, $5)`,
		challenge.ID, challenge.UserID, challenge.SessionData, string(challenge.Kind), challenge.ExpiresAt,
	)
	return err
}

func (s *Store) ConsumePasskeyChallenge(ctx context.Context, id string) (core.PasskeyChallenge, error) {
	var challenge core.PasskeyChallenge
	var kind string
	err := s.conn(ctx).QueryRow(ctx,
		`DELETE FROM webauthn_challenges WHERE id = $1
		 RETURNING id, user_id, session_data, type, expires_at`,
		id,
	).Scan(&challenge.ID, &challenge.UserID, &challenge.SessionData, &kind, &challenge.ExpiresAt)
	if errors.Is(err, errNoRows) {
		return core.PasskeyChallenge{}, core.ErrInvalidPasskeyChallenge
	}
	challenge.Kind = core.PasskeyChallengeKind(kind)
	return challenge, err
}

func (s *Store) DeleteExpiredPasskeyChallenges(ctx context.Context, before time.Time) error {
	_, err := s.conn(ctx).Exec(ctx, `DELETE FROM webauthn_challenges WHERE expires_at <= $1`, before)
	return err
}
