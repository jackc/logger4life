ALTER TABLE logs ADD COLUMN fields jsonb NOT NULL DEFAULT '[]';
ALTER TABLE log_entries ADD COLUMN fields jsonb NOT NULL DEFAULT '{}';

---- create above / drop below ----

ALTER TABLE log_entries DROP COLUMN fields;
ALTER TABLE logs DROP COLUMN fields;
