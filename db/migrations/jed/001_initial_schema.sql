-- Logger4Life's embedded schema mirrors the PostgreSQL schema at the port
-- boundary. Identifiers are stored as canonical UUID text so Go's string IDs
-- bind and scan without a database-specific UUID adapter.

CREATE TABLE users (
    id text PRIMARY KEY,
    username varchar(30) NOT NULL CHECK (username ~ '\A[a-zA-Z0-9_]+\z'),
    email varchar(254),
    password_hash varchar(255) NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX users_username_unq ON users (lower(username));
CREATE UNIQUE INDEX users_email_unq ON users (lower(email)) WHERE email IS NOT NULL;

CREATE TABLE sessions (
    id text PRIMARY KEY,
    user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token bytea NOT NULL UNIQUE,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL DEFAULT (now() + interval '30 days')
);
CREATE INDEX sessions_user_id_idx ON sessions (user_id);
CREATE INDEX sessions_expires_at_idx ON sessions (expires_at);

-- The public names logs and log_entries are reserved for the filtered
-- session-local tables used by the user-authored SQL feature.
CREATE TABLE all_logs (
    id text PRIMARY KEY,
    user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name varchar(100) NOT NULL CHECK (char_length(name) > 0),
    fields jsonb NOT NULL DEFAULT '[]',
    share_token bytea,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX logs_user_id_idx ON all_logs (user_id);
CREATE UNIQUE INDEX logs_user_id_name_unq ON all_logs (user_id, lower(name));
CREATE UNIQUE INDEX logs_share_token_unq ON all_logs (share_token) WHERE share_token IS NOT NULL;

CREATE TABLE all_log_entries (
    id text PRIMARY KEY,
    log_id text NOT NULL REFERENCES all_logs(id),
    user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    fields jsonb NOT NULL DEFAULT '{}',
    occurred_at timestamptz NOT NULL DEFAULT now(),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX log_entries_log_id_created_at_idx ON all_log_entries (log_id, created_at);
CREATE INDEX log_entries_log_id_occurred_at_idx ON all_log_entries (log_id, occurred_at);
CREATE INDEX log_entries_user_id_idx ON all_log_entries (user_id);

CREATE TABLE log_shares (
    id text PRIMARY KEY,
    log_id text NOT NULL REFERENCES all_logs(id),
    user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX log_shares_log_id_user_id_unq ON log_shares (log_id, user_id);
CREATE INDEX log_shares_user_id_idx ON log_shares (user_id);

CREATE TABLE passkeys (
    id text PRIMARY KEY,
    user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
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

CREATE TABLE webauthn_challenges (
    id text PRIMARY KEY,
    user_id text REFERENCES users(id) ON DELETE CASCADE,
    session_data bytea NOT NULL,
    type varchar(20) NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL DEFAULT (now() + interval '5 minutes')
);
CREATE INDEX webauthn_challenges_expires_at_idx ON webauthn_challenges (expires_at);

CREATE TABLE saved_sql_queries (
    id text PRIMARY KEY,
    user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name text NOT NULL,
    query_text text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (user_id, name)
);
CREATE INDEX saved_sql_queries_user_id_idx ON saved_sql_queries (user_id);

CREATE TABLE oauth_clients (
    id text PRIMARY KEY,
    redirect_uris jsonb NOT NULL,
    client_name text,
    client_uri text,
    logo_uri text,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE oauth_authorization_codes (
    code_hash bytea PRIMARY KEY,
    client_id text NOT NULL REFERENCES oauth_clients(id) ON DELETE CASCADE,
    user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    redirect_uri text NOT NULL,
    scope text NOT NULL,
    audience text NOT NULL,
    code_challenge text NOT NULL,
    code_challenge_method text NOT NULL,
    expires_at timestamptz NOT NULL,
    used boolean NOT NULL DEFAULT false
);
CREATE INDEX oauth_authorization_codes_user_id_idx ON oauth_authorization_codes (user_id);
CREATE INDEX oauth_authorization_codes_expires_at_idx ON oauth_authorization_codes (expires_at);

CREATE TABLE oauth_access_tokens (
    token_hash bytea PRIMARY KEY,
    client_id text NOT NULL REFERENCES oauth_clients(id) ON DELETE CASCADE,
    user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    refresh_token_hash bytea,
    family_id text NOT NULL,
    scope text NOT NULL,
    audience text NOT NULL,
    expires_at timestamptz NOT NULL
);
CREATE INDEX oauth_access_tokens_user_id_idx ON oauth_access_tokens (user_id);
CREATE INDEX oauth_access_tokens_refresh_idx ON oauth_access_tokens (refresh_token_hash);
CREATE INDEX oauth_access_tokens_family_idx ON oauth_access_tokens (family_id);
CREATE INDEX oauth_access_tokens_expires_at_idx ON oauth_access_tokens (expires_at);

CREATE TABLE oauth_refresh_tokens (
    token_hash bytea PRIMARY KEY,
    client_id text NOT NULL REFERENCES oauth_clients(id) ON DELETE CASCADE,
    user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    family_id text NOT NULL,
    scope text NOT NULL,
    audience text NOT NULL,
    expires_at timestamptz NOT NULL,
    revoked boolean NOT NULL DEFAULT false
);
CREATE INDEX oauth_refresh_tokens_user_id_idx ON oauth_refresh_tokens (user_id);
CREATE INDEX oauth_refresh_tokens_family_idx ON oauth_refresh_tokens (family_id);
CREATE INDEX oauth_refresh_tokens_expires_at_idx ON oauth_refresh_tokens (expires_at);

CREATE TABLE folders (
    id text PRIMARY KEY,
    user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    parent_folder_id text REFERENCES folders(id),
    name varchar(100) NOT NULL CHECK (char_length(name) > 0),
    position integer NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX folders_user_parent_position_idx ON folders (user_id, parent_folder_id, position);

CREATE TABLE user_log_placements (
    user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    log_id text NOT NULL REFERENCES all_logs(id),
    folder_id text REFERENCES folders(id),
    position integer NOT NULL,
    pinned_to_home boolean NOT NULL DEFAULT true,
    home_position integer NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, log_id)
);
CREATE INDEX user_log_placements_user_folder_position_idx
    ON user_log_placements (user_id, folder_id, position);
CREATE INDEX user_log_placements_user_home_position_idx
    ON user_log_placements (user_id, home_position) WHERE pinned_to_home;

---- create above / drop below ----

DROP TABLE user_log_placements;
DROP TABLE folders;
DROP TABLE oauth_refresh_tokens;
DROP TABLE oauth_access_tokens;
DROP TABLE oauth_authorization_codes;
DROP TABLE oauth_clients;
DROP TABLE saved_sql_queries;
DROP TABLE webauthn_challenges;
DROP TABLE passkeys;
DROP TABLE log_shares;
DROP TABLE all_log_entries;
DROP TABLE all_logs;
DROP TABLE sessions;
DROP TABLE users;
