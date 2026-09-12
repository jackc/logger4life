CREATE TABLE oauth_token_families (
    id text PRIMARY KEY,
    client_id text NOT NULL REFERENCES oauth_clients(id) ON DELETE CASCADE,
    user_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    revoked boolean NOT NULL DEFAULT false
);
CREATE INDEX oauth_token_families_client_id_idx ON oauth_token_families (client_id);
CREATE INDEX oauth_token_families_user_id_idx ON oauth_token_families (user_id);

INSERT INTO oauth_token_families (id, client_id, user_id)
SELECT DISTINCT family_id, client_id, user_id FROM oauth_refresh_tokens;

INSERT INTO oauth_token_families (id, client_id, user_id)
SELECT DISTINCT a.family_id, a.client_id, a.user_id FROM oauth_access_tokens a
WHERE NOT EXISTS (SELECT 1 FROM oauth_token_families f WHERE f.id = a.family_id);

---- create above / drop below ----

DROP TABLE oauth_token_families;
