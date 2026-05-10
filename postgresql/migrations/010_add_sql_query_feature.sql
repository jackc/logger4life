-- Restricted role used by the SQL Query feature. NOINHERIT means the app role
-- does not implicitly gain these privileges; it must SET ROLE explicitly.
-- Roles are cluster-wide, so the role may already exist from a sibling database.
DO $$
BEGIN
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'logger4life_sql_user') THEN
        CREATE ROLE logger4life_sql_user NOINHERIT;
    END IF;
END
$$;
GRANT logger4life_sql_user TO {{.app_user}};

-- Defensive: the restricted role gets no public schema access.
REVOKE ALL ON SCHEMA public FROM logger4life_sql_user;

CREATE SCHEMA sql_query;
GRANT USAGE ON SCHEMA sql_query TO logger4life_sql_user;

-- Views run with the owner's privileges (default security_invoker = false on
-- PG 15+), so they can read public.logs / public.log_entries / public.users /
-- public.log_shares even though the querying role cannot. Filtering is by
-- current_setting('app.current_user_id'), set per-request before SET ROLE.

CREATE VIEW sql_query.logs AS
SELECT
    l.id,
    l.name,
    l.fields,
    l.created_at,
    l.updated_at,
    CASE
        WHEN l.user_id = current_setting('app.current_user_id')::uuid THEN
            (SELECT COALESCE(array_agg(u.username::text ORDER BY u.username), ARRAY[]::text[])
             FROM public.log_shares ls
             JOIN public.users u ON u.id = ls.user_id
             WHERE ls.log_id = l.id)
        ELSE NULL
    END AS shared_with
FROM public.logs l
WHERE l.user_id = current_setting('app.current_user_id')::uuid
   OR EXISTS (
        SELECT 1 FROM public.log_shares ls
        WHERE ls.log_id = l.id
          AND ls.user_id = current_setting('app.current_user_id')::uuid
   );

CREATE VIEW sql_query.log_entries AS
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

GRANT SELECT ON sql_query.logs TO logger4life_sql_user;
GRANT SELECT ON sql_query.log_entries TO logger4life_sql_user;

COMMENT ON VIEW sql_query.logs IS 'Logs you own or have been shared on.';
COMMENT ON COLUMN sql_query.logs.id IS 'UUID identifying the log.';
COMMENT ON COLUMN sql_query.logs.name IS 'Display name of the log.';
COMMENT ON COLUMN sql_query.logs.fields IS 'JSONB array of field definitions: [{"name","type","required"}].';
COMMENT ON COLUMN sql_query.logs.created_at IS 'When the log was created.';
COMMENT ON COLUMN sql_query.logs.updated_at IS 'When the log was last updated.';
COMMENT ON COLUMN sql_query.logs.shared_with IS 'Array of usernames the log is shared with. NULL unless you are the log owner.';

COMMENT ON VIEW sql_query.log_entries IS 'Entries from logs you own or have been shared on.';
COMMENT ON COLUMN sql_query.log_entries.id IS 'UUID identifying the entry.';
COMMENT ON COLUMN sql_query.log_entries.log_id IS 'UUID of the parent log (join to logs.id).';
COMMENT ON COLUMN sql_query.log_entries.user_id IS 'UUID of the user who created the entry.';
COMMENT ON COLUMN sql_query.log_entries.user_username IS 'Username of the user who created the entry.';
COMMENT ON COLUMN sql_query.log_entries.fields IS 'JSONB object with the entry''s field values, keyed by field name.';
COMMENT ON COLUMN sql_query.log_entries.occurred_at IS 'When the event being logged occurred.';
COMMENT ON COLUMN sql_query.log_entries.created_at IS 'When the entry record was created.';
COMMENT ON COLUMN sql_query.log_entries.updated_at IS 'When the entry record was last updated.';

CREATE TABLE saved_sql_queries (
    id uuid PRIMARY KEY DEFAULT uuidv7(),
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name text NOT NULL,
    query_text text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (user_id, name)
);

CREATE INDEX saved_sql_queries_user_id_idx ON saved_sql_queries (user_id);

GRANT SELECT, INSERT, UPDATE, DELETE ON saved_sql_queries TO {{.app_user}};

---- create above / drop below ----

DROP TABLE saved_sql_queries;
DROP VIEW sql_query.log_entries;
DROP VIEW sql_query.logs;
DROP SCHEMA sql_query;
-- Role is cluster-wide; only drop if it has no remaining grants in other databases.
-- If this fails, manually revoke remaining privileges first.
DROP ROLE IF EXISTS logger4life_sql_user;
