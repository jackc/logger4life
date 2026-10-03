ALTER TABLE log_entries ADD COLUMN note text NOT NULL DEFAULT '' CHECK (char_length(note) <= 20000);

CREATE OR REPLACE VIEW sql_query.log_entries AS
SELECT
    le.id,
    le.log_id,
    le.user_id,
    u.username AS user_username,
    le.fields,
    le.occurred_at,
    le.created_at,
    le.updated_at,
    le.note
FROM public.log_entries le
JOIN public.users u ON u.id = le.user_id
WHERE le.log_id IN (SELECT id FROM sql_query.logs);
COMMENT ON COLUMN sql_query.log_entries.note IS 'Optional Markdown note for the entry.';

---- create above / drop below ----

DROP VIEW sql_query.log_entries;
CREATE OR REPLACE VIEW sql_query.log_entries AS
SELECT
    le.id,
    le.log_id,
    le.user_id,
    u.username AS user_username,
    le.fields,
    le.occurred_at,
    le.created_at,
    le.updated_at
FROM public.log_entries le
JOIN public.users u ON u.id = le.user_id
WHERE le.log_id IN (SELECT id FROM sql_query.logs);
GRANT SELECT ON sql_query.log_entries TO logger4life_sql_user;
COMMENT ON VIEW sql_query.log_entries IS 'Entries from logs you own or have been shared on.';
COMMENT ON COLUMN sql_query.log_entries.id IS 'UUID identifying the entry.';
COMMENT ON COLUMN sql_query.log_entries.log_id IS 'UUID of the parent log (join to logs.id).';
COMMENT ON COLUMN sql_query.log_entries.user_id IS 'UUID of the user who created the entry.';
COMMENT ON COLUMN sql_query.log_entries.user_username IS 'Username of the user who created the entry.';
COMMENT ON COLUMN sql_query.log_entries.fields IS 'JSONB object with the entry''s field values, keyed by field name.';
COMMENT ON COLUMN sql_query.log_entries.occurred_at IS 'When the event being logged occurred.';
COMMENT ON COLUMN sql_query.log_entries.created_at IS 'When the entry record was created.';
COMMENT ON COLUMN sql_query.log_entries.updated_at IS 'When the entry record was last updated.';

ALTER TABLE log_entries DROP COLUMN note;
