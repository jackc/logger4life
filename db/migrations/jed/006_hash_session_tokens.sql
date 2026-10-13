-- Session tokens are stored as their SHA-256. Existing sessions survive:
-- each stored token is replaced by its hash.
ALTER TABLE sessions RENAME COLUMN token TO token_hash;
UPDATE sessions SET token_hash = sha256(token_hash);

---- create above / drop below ----

-- A hash cannot be turned back into its token, so reverting ends every
-- session.
DELETE FROM sessions;
ALTER TABLE sessions RENAME COLUMN token_hash TO token;
