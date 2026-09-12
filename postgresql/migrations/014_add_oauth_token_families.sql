-- A revoked family must reject tokens issued by an in-flight refresh after
-- reuse detection. Keep that decision independently of its existing tokens.
CREATE TABLE oauth_token_families (
    id uuid PRIMARY KEY,
    client_id text NOT NULL REFERENCES oauth_clients(id) ON DELETE CASCADE,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    revoked boolean NOT NULL DEFAULT false
);
CREATE INDEX oauth_token_families_client_id_idx ON oauth_token_families (client_id);
CREATE INDEX oauth_token_families_user_id_idx ON oauth_token_families (user_id);

INSERT INTO oauth_token_families (id, client_id, user_id)
SELECT family_id, client_id, user_id FROM oauth_refresh_tokens
UNION
SELECT family_id, client_id, user_id FROM oauth_access_tokens;

GRANT SELECT, INSERT, UPDATE, DELETE ON oauth_token_families TO {{.app_user}};

---- create above / drop below ----

DROP TABLE oauth_token_families;
