-- Folders organize logs into a per-user, optionally nested tree. Logs are
-- shared between users, so placement (folder + position) is stored per-user
-- rather than directly on the log row.

CREATE TABLE folders (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    parent_folder_id uuid REFERENCES folders(id) ON DELETE CASCADE,
    name varchar(100) NOT NULL CHECK (char_length(trim(name)) > 0),
    position integer NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX folders_user_parent_position_idx
    ON folders (user_id, parent_folder_id, position);

GRANT SELECT, INSERT, UPDATE, DELETE ON folders TO {{.app_user}};

CREATE TABLE user_log_placements (
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    log_id uuid NOT NULL REFERENCES logs(id) ON DELETE CASCADE,
    folder_id uuid REFERENCES folders(id) ON DELETE SET NULL,
    position integer NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, log_id)
);

CREATE INDEX user_log_placements_user_folder_position_idx
    ON user_log_placements (user_id, folder_id, position);

GRANT SELECT, INSERT, UPDATE, DELETE ON user_log_placements TO {{.app_user}};

-- Backfill: one placement row per (user, log) pair, ordered alphabetically
-- so the existing display order is preserved on first deploy.
INSERT INTO user_log_placements (user_id, log_id, folder_id, position)
SELECT user_id, log_id, NULL,
       (row_number() OVER (PARTITION BY user_id ORDER BY lower(name), log_id))::int - 1
FROM (
    SELECT l.user_id, l.id AS log_id, l.name FROM logs l
    UNION ALL
    SELECT ls.user_id, ls.log_id, l.name
    FROM log_shares ls JOIN logs l ON l.id = ls.log_id
) t;

---- create above / drop below ----

DROP TABLE user_log_placements;
DROP TABLE folders;
