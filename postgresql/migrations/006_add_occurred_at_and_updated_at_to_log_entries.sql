ALTER TABLE log_entries ADD COLUMN occurred_at timestamptz NOT NULL DEFAULT now();
ALTER TABLE log_entries ADD COLUMN updated_at timestamptz NOT NULL DEFAULT now();

-- Backfill: set occurred_at to created_at for existing rows
UPDATE log_entries SET occurred_at = created_at;

-- New index for querying entries by occurrence time
CREATE INDEX log_entries_log_id_occurred_at_idx ON log_entries (log_id, occurred_at DESC);

---- create above / drop below ----

DROP INDEX log_entries_log_id_occurred_at_idx;
ALTER TABLE log_entries DROP COLUMN updated_at;
ALTER TABLE log_entries DROP COLUMN occurred_at;
