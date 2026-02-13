CREATE TABLE logs (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name varchar(100) NOT NULL CHECK (char_length(trim(name)) > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX logs_user_id_idx ON logs (user_id);
CREATE UNIQUE INDEX logs_user_id_name_unq ON logs (user_id, lower(name));

GRANT SELECT, INSERT, UPDATE, DELETE ON logs TO {{.app_user}};

---- create above / drop below ----

DROP TABLE logs;
