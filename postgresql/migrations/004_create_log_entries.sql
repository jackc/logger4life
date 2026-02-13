CREATE TABLE log_entries (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    log_id uuid NOT NULL REFERENCES logs(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX log_entries_log_id_created_at_idx ON log_entries (log_id, created_at DESC);

GRANT SELECT, INSERT, UPDATE, DELETE ON log_entries TO {{.app_user}};

---- create above / drop below ----

DROP TABLE log_entries;
