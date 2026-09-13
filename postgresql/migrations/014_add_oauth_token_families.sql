-- 011 gained family_id in place after it had already been applied elsewhere,
-- so a database reaching this migration may still be without the column. Add
-- it where it is missing rather than assume 011 left it behind.
ALTER TABLE oauth_access_tokens ADD COLUMN IF NOT EXISTS family_id uuid;
ALTER TABLE oauth_refresh_tokens ADD COLUMN IF NOT EXISTS family_id uuid;

-- Tokens issued before the column existed still have to belong somewhere. An
-- access token joins the family of the refresh token it was issued with, so a
-- reuse detection still revokes the pair together; every other token starts a
-- family of its own, which is the narrowest grouping its history supports.
UPDATE oauth_refresh_tokens SET family_id = uuidv7() WHERE family_id IS NULL;
UPDATE oauth_access_tokens a SET family_id = COALESCE(
    (SELECT r.family_id FROM oauth_refresh_tokens r WHERE r.token_hash = a.refresh_token_hash),
    uuidv7())
WHERE a.family_id IS NULL;

ALTER TABLE oauth_access_tokens ALTER COLUMN family_id SET NOT NULL;
ALTER TABLE oauth_refresh_tokens ALTER COLUMN family_id SET NOT NULL;
CREATE INDEX IF NOT EXISTS oauth_access_tokens_family_idx ON oauth_access_tokens (family_id);
CREATE INDEX IF NOT EXISTS oauth_refresh_tokens_family_idx ON oauth_refresh_tokens (family_id);

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

-- Rotations share a family, and an access token can outlive the refresh token
-- that issued it, so each family is inserted once from whichever table has it.
INSERT INTO oauth_token_families (id, client_id, user_id)
SELECT DISTINCT family_id, client_id, user_id FROM oauth_refresh_tokens;

INSERT INTO oauth_token_families (id, client_id, user_id)
SELECT DISTINCT a.family_id, a.client_id, a.user_id FROM oauth_access_tokens a
WHERE NOT EXISTS (SELECT 1 FROM oauth_token_families f WHERE f.id = a.family_id);

GRANT SELECT, INSERT, UPDATE, DELETE ON oauth_token_families TO {{.app_user}};

---- create above / drop below ----

-- family_id is deliberately left in place: on a database where 011 created
-- it, dropping it here would undo a migration this one does not own.
DROP TABLE oauth_token_families;
