-- Existing DCR grants keep refresh support. CIMD codes snapshot the grant
-- types approved from metadata so later document edits cannot expand a grant.
ALTER TABLE oauth_authorization_codes ADD COLUMN authorization_code_only boolean NOT NULL DEFAULT false;

---- create above / drop below ----

ALTER TABLE oauth_authorization_codes DROP COLUMN authorization_code_only;
