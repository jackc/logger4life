-- Ensure the test database and app role exist.
\i postgresql/prepare.sql

-- Clean the test database so tern can re-run migrations from scratch.
\c logger4life_test
DROP TABLE IF EXISTS oauth_refresh_tokens CASCADE;
DROP TABLE IF EXISTS oauth_access_tokens CASCADE;
DROP TABLE IF EXISTS oauth_authorization_codes CASCADE;
DROP TABLE IF EXISTS oauth_clients CASCADE;
DROP TABLE IF EXISTS saved_sql_queries CASCADE;
DROP SCHEMA IF EXISTS sql_query CASCADE;
DROP TABLE IF EXISTS webauthn_challenges CASCADE;
DROP TABLE IF EXISTS passkeys CASCADE;
DROP TABLE IF EXISTS user_log_placements CASCADE;
DROP TABLE IF EXISTS folders CASCADE;
DROP TABLE IF EXISTS log_shares CASCADE;
DROP TABLE IF EXISTS log_entries CASCADE;
DROP TABLE IF EXISTS logs CASCADE;
DROP TABLE IF EXISTS sessions CASCADE;
DROP TABLE IF EXISTS users CASCADE;
DROP TABLE IF EXISTS schema_version CASCADE;
-- The cluster-wide logger4life_sql_user role is preserved between test runs;
-- the migration's CREATE ROLE is guarded by an existence check.
