-- Ensure the test database and app role exist.
\i postgresql/prepare.sql

-- Clean the test database so tern can re-run migrations from scratch.
\c logger4life_test
DROP TABLE IF EXISTS log_shares CASCADE;
DROP TABLE IF EXISTS log_entries CASCADE;
DROP TABLE IF EXISTS logs CASCADE;
DROP TABLE IF EXISTS sessions CASCADE;
DROP TABLE IF EXISTS users CASCADE;
DROP TABLE IF EXISTS schema_version CASCADE;
