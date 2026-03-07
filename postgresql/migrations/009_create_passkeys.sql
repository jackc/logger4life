CREATE TABLE passkeys (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    credential_id bytea NOT NULL UNIQUE,
    public_key bytea NOT NULL,
    aaguid bytea NOT NULL,
    sign_count bigint NOT NULL DEFAULT 0,
    backup_eligible boolean NOT NULL DEFAULT false,
    backup_state boolean NOT NULL DEFAULT false,
    description varchar(100) NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX passkeys_user_id_idx ON passkeys (user_id);

GRANT SELECT, INSERT, UPDATE, DELETE ON passkeys TO {{.app_user}};

CREATE TABLE webauthn_challenges (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    user_id uuid REFERENCES users(id) ON DELETE CASCADE,
    session_data bytea NOT NULL,
    type varchar(20) NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL DEFAULT (now() + interval '5 minutes')
);

CREATE INDEX webauthn_challenges_expires_at_idx ON webauthn_challenges (expires_at);

GRANT SELECT, INSERT, UPDATE, DELETE ON webauthn_challenges TO {{.app_user}};

---- create above / drop below ----

DROP TABLE webauthn_challenges;
DROP TABLE passkeys;
