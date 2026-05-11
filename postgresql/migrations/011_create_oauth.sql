-- OAuth 2.1 authorization server tables backing the MCP integration.
-- Tokens are opaque random strings; we only persist their sha256 hash so a
-- DB leak does not expose live tokens.

CREATE TABLE oauth_clients (
    id text PRIMARY KEY,
    redirect_uris text[] NOT NULL,
    client_name text,
    client_uri text,
    logo_uri text,
    created_at timestamptz NOT NULL DEFAULT now()
);

GRANT SELECT, INSERT, UPDATE, DELETE ON oauth_clients TO {{.app_user}};

CREATE TABLE oauth_authorization_codes (
    code_hash bytea PRIMARY KEY,
    client_id text NOT NULL REFERENCES oauth_clients(id) ON DELETE CASCADE,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
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

GRANT SELECT, INSERT, UPDATE, DELETE ON oauth_authorization_codes TO {{.app_user}};

-- family_id identifies the chain of refresh + access tokens originating from
-- a single authorization grant. Carried forward across refresh rotations so
-- that a detected refresh-token reuse can revoke the entire family.
CREATE TABLE oauth_access_tokens (
    token_hash bytea PRIMARY KEY,
    client_id text NOT NULL REFERENCES oauth_clients(id) ON DELETE CASCADE,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    refresh_token_hash bytea,
    family_id uuid NOT NULL,
    scope text NOT NULL,
    audience text NOT NULL,
    expires_at timestamptz NOT NULL
);
CREATE INDEX oauth_access_tokens_user_id_idx ON oauth_access_tokens (user_id);
CREATE INDEX oauth_access_tokens_refresh_idx ON oauth_access_tokens (refresh_token_hash);
CREATE INDEX oauth_access_tokens_family_idx ON oauth_access_tokens (family_id);
CREATE INDEX oauth_access_tokens_expires_at_idx ON oauth_access_tokens (expires_at);

GRANT SELECT, INSERT, UPDATE, DELETE ON oauth_access_tokens TO {{.app_user}};

CREATE TABLE oauth_refresh_tokens (
    token_hash bytea PRIMARY KEY,
    client_id text NOT NULL REFERENCES oauth_clients(id) ON DELETE CASCADE,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    family_id uuid NOT NULL,
    scope text NOT NULL,
    audience text NOT NULL,
    expires_at timestamptz NOT NULL,
    revoked boolean NOT NULL DEFAULT false
);
CREATE INDEX oauth_refresh_tokens_user_id_idx ON oauth_refresh_tokens (user_id);
CREATE INDEX oauth_refresh_tokens_family_idx ON oauth_refresh_tokens (family_id);
CREATE INDEX oauth_refresh_tokens_expires_at_idx ON oauth_refresh_tokens (expires_at);

GRANT SELECT, INSERT, UPDATE, DELETE ON oauth_refresh_tokens TO {{.app_user}};

---- create above / drop below ----

DROP TABLE oauth_refresh_tokens;
DROP TABLE oauth_access_tokens;
DROP TABLE oauth_authorization_codes;
DROP TABLE oauth_clients;
