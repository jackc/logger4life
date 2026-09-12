-- Existing DCR codes retain refresh support.
ALTER TABLE oauth_authorization_codes ADD COLUMN authorization_code_only boolean NOT NULL DEFAULT false;

---- create above / drop below ----

ALTER TABLE oauth_authorization_codes DROP COLUMN authorization_code_only;
