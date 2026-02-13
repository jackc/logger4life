ALTER TABLE logs ADD COLUMN share_token bytea;
CREATE UNIQUE INDEX logs_share_token_unq ON logs (share_token) WHERE share_token IS NOT NULL;

CREATE TABLE log_shares (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    log_id uuid NOT NULL REFERENCES logs(id) ON DELETE CASCADE,
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX log_shares_log_id_user_id_unq ON log_shares (log_id, user_id);
CREATE INDEX log_shares_user_id_idx ON log_shares (user_id);

GRANT SELECT, INSERT, UPDATE, DELETE ON log_shares TO {{.app_user}};

---- create above / drop below ----

DROP TABLE log_shares;
DROP INDEX IF EXISTS logs_share_token_unq;
ALTER TABLE logs DROP COLUMN share_token;
