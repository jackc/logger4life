-- Create user 'logger4life' if it doesn't exist
DO
$$
BEGIN
  IF NOT EXISTS (
    SELECT FROM pg_catalog.pg_roles
    WHERE rolname = 'logger4life'
  ) THEN
    CREATE ROLE logger4life WITH LOGIN PASSWORD 'password';
  END IF;
END
$$;

-- Create development database if it doesn't exist
SELECT 'CREATE DATABASE logger4life_dev'
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'logger4life_dev')\gexec

-- Create test database if it doesn't exist
SELECT 'CREATE DATABASE logger4life_test'
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'logger4life_test')\gexec
