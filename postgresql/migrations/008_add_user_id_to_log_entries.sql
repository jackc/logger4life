ALTER TABLE log_entries ADD COLUMN user_id uuid REFERENCES users(id) ON DELETE CASCADE;

UPDATE log_entries SET user_id = logs.user_id
FROM logs WHERE log_entries.log_id = logs.id;

ALTER TABLE log_entries ALTER COLUMN user_id SET NOT NULL;

CREATE INDEX log_entries_user_id_idx ON log_entries (user_id);

---- create above / drop below ----

ALTER TABLE log_entries DROP COLUMN user_id;
