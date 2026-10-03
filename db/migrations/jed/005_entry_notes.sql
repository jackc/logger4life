ALTER TABLE all_log_entries ADD COLUMN note text NOT NULL DEFAULT '' CHECK (char_length(note) <= 20000);

---- create above / drop below ----

ALTER TABLE all_log_entries DROP COLUMN note;
