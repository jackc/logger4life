-- OAuth 2.1 authorization server tables backing the MCP integration.
-- Token storage uses fosite-style "signatures" (the HMAC-derived opaque
-- string fosite computes from each token) as the lookup key; the raw token
-- itself never lands in the database.

CREATE TABLE oauth_clients (
    id text PRIMARY KEY,
    redirect_uris text[] NOT NULL DEFAULT ARRAY[]::text[],
    grant_types text[] NOT NULL DEFAULT ARRAY['authorization_code','refresh_token']::text[],
    response_types text[] NOT NULL DEFAULT ARRAY['code']::text[],
    scopes text[] NOT NULL DEFAULT ARRAY[]::text[],
    audiences text[] NOT NULL DEFAULT ARRAY[]::text[],
    token_endpoint_auth_method text NOT NULL DEFAULT 'none',
    client_name text,
    client_uri text,
    logo_uri text,
    created_at timestamptz NOT NULL DEFAULT now()
);

GRANT SELECT, INSERT, UPDATE, DELETE ON oauth_clients TO {{.app_user}};

-- Each token-kind table has the same shape so we can swap a single helper
-- in oauth_storage.go. The `active` column flips false on invalidation
-- (fosite needs to detect replays of already-used codes/refresh tokens).

CREATE TABLE oauth_authorize_codes (
    signature text PRIMARY KEY,
    request_id text NOT NULL,
    client_id text NOT NULL REFERENCES oauth_clients(id) ON DELETE CASCADE,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    requested_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    requested_scopes text[] NOT NULL DEFAULT ARRAY[]::text[],
    granted_scopes text[] NOT NULL DEFAULT ARRAY[]::text[],
    requested_audience text[] NOT NULL DEFAULT ARRAY[]::text[],
    granted_audience text[] NOT NULL DEFAULT ARRAY[]::text[],
    form jsonb NOT NULL DEFAULT '{}'::jsonb,
    session jsonb NOT NULL DEFAULT '{}'::jsonb,
    active boolean NOT NULL DEFAULT true
);
CREATE INDEX oauth_authorize_codes_request_id_idx ON oauth_authorize_codes (request_id);
CREATE INDEX oauth_authorize_codes_user_id_idx ON oauth_authorize_codes (user_id);
CREATE INDEX oauth_authorize_codes_expires_at_idx ON oauth_authorize_codes (expires_at);

GRANT SELECT, INSERT, UPDATE, DELETE ON oauth_authorize_codes TO {{.app_user}};

CREATE TABLE oauth_access_tokens (
    signature text PRIMARY KEY,
    request_id text NOT NULL,
    client_id text NOT NULL REFERENCES oauth_clients(id) ON DELETE CASCADE,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    requested_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    requested_scopes text[] NOT NULL DEFAULT ARRAY[]::text[],
    granted_scopes text[] NOT NULL DEFAULT ARRAY[]::text[],
    requested_audience text[] NOT NULL DEFAULT ARRAY[]::text[],
    granted_audience text[] NOT NULL DEFAULT ARRAY[]::text[],
    form jsonb NOT NULL DEFAULT '{}'::jsonb,
    session jsonb NOT NULL DEFAULT '{}'::jsonb,
    active boolean NOT NULL DEFAULT true
);
CREATE INDEX oauth_access_tokens_request_id_idx ON oauth_access_tokens (request_id);
CREATE INDEX oauth_access_tokens_user_id_idx ON oauth_access_tokens (user_id);
CREATE INDEX oauth_access_tokens_expires_at_idx ON oauth_access_tokens (expires_at);

GRANT SELECT, INSERT, UPDATE, DELETE ON oauth_access_tokens TO {{.app_user}};

CREATE TABLE oauth_refresh_tokens (
    signature text PRIMARY KEY,
    request_id text NOT NULL,
    client_id text NOT NULL REFERENCES oauth_clients(id) ON DELETE CASCADE,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    requested_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    requested_scopes text[] NOT NULL DEFAULT ARRAY[]::text[],
    granted_scopes text[] NOT NULL DEFAULT ARRAY[]::text[],
    requested_audience text[] NOT NULL DEFAULT ARRAY[]::text[],
    granted_audience text[] NOT NULL DEFAULT ARRAY[]::text[],
    form jsonb NOT NULL DEFAULT '{}'::jsonb,
    session jsonb NOT NULL DEFAULT '{}'::jsonb,
    active boolean NOT NULL DEFAULT true
);
CREATE INDEX oauth_refresh_tokens_request_id_idx ON oauth_refresh_tokens (request_id);
CREATE INDEX oauth_refresh_tokens_user_id_idx ON oauth_refresh_tokens (user_id);
CREATE INDEX oauth_refresh_tokens_expires_at_idx ON oauth_refresh_tokens (expires_at);

GRANT SELECT, INSERT, UPDATE, DELETE ON oauth_refresh_tokens TO {{.app_user}};

-- PKCE sessions are keyed by the authorization-code signature, not by a
-- separate token. Fosite uses this to validate the code_verifier on the
-- token request matches the code_challenge from the authorize request.
CREATE TABLE oauth_pkce_sessions (
    signature text PRIMARY KEY,
    request_id text NOT NULL,
    client_id text NOT NULL REFERENCES oauth_clients(id) ON DELETE CASCADE,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    requested_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    requested_scopes text[] NOT NULL DEFAULT ARRAY[]::text[],
    granted_scopes text[] NOT NULL DEFAULT ARRAY[]::text[],
    requested_audience text[] NOT NULL DEFAULT ARRAY[]::text[],
    granted_audience text[] NOT NULL DEFAULT ARRAY[]::text[],
    form jsonb NOT NULL DEFAULT '{}'::jsonb,
    session jsonb NOT NULL DEFAULT '{}'::jsonb,
    active boolean NOT NULL DEFAULT true
);
CREATE INDEX oauth_pkce_sessions_expires_at_idx ON oauth_pkce_sessions (expires_at);

GRANT SELECT, INSERT, UPDATE, DELETE ON oauth_pkce_sessions TO {{.app_user}};

---- create above / drop below ----

DROP TABLE oauth_pkce_sessions;
DROP TABLE oauth_refresh_tokens;
DROP TABLE oauth_access_tokens;
DROP TABLE oauth_authorize_codes;
DROP TABLE oauth_clients;
