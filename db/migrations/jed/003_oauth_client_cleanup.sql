CREATE INDEX oauth_clients_created_at_idx ON oauth_clients (created_at);
CREATE INDEX oauth_authorization_codes_client_id_idx ON oauth_authorization_codes (client_id);
CREATE INDEX oauth_access_tokens_client_id_idx ON oauth_access_tokens (client_id);
CREATE INDEX oauth_refresh_tokens_client_id_idx ON oauth_refresh_tokens (client_id);


-- Clients with grant history must never be removed by registration retention.
ALTER TABLE oauth_authorization_codes DROP CONSTRAINT oauth_authorization_codes_client_id_fkey;
ALTER TABLE oauth_authorization_codes ADD CONSTRAINT oauth_authorization_codes_client_id_fkey FOREIGN KEY (client_id) REFERENCES oauth_clients(id) ON DELETE RESTRICT;
ALTER TABLE oauth_access_tokens DROP CONSTRAINT oauth_access_tokens_client_id_fkey;
ALTER TABLE oauth_access_tokens ADD CONSTRAINT oauth_access_tokens_client_id_fkey FOREIGN KEY (client_id) REFERENCES oauth_clients(id) ON DELETE RESTRICT;
ALTER TABLE oauth_refresh_tokens DROP CONSTRAINT oauth_refresh_tokens_client_id_fkey;
ALTER TABLE oauth_refresh_tokens ADD CONSTRAINT oauth_refresh_tokens_client_id_fkey FOREIGN KEY (client_id) REFERENCES oauth_clients(id) ON DELETE RESTRICT;
ALTER TABLE oauth_token_families DROP CONSTRAINT oauth_token_families_client_id_fkey;
ALTER TABLE oauth_token_families ADD CONSTRAINT oauth_token_families_client_id_fkey FOREIGN KEY (client_id) REFERENCES oauth_clients(id) ON DELETE RESTRICT;

---- create above / drop below ----

ALTER TABLE oauth_authorization_codes DROP CONSTRAINT oauth_authorization_codes_client_id_fkey;
ALTER TABLE oauth_authorization_codes ADD CONSTRAINT oauth_authorization_codes_client_id_fkey FOREIGN KEY (client_id) REFERENCES oauth_clients(id) ON DELETE CASCADE;
ALTER TABLE oauth_access_tokens DROP CONSTRAINT oauth_access_tokens_client_id_fkey;
ALTER TABLE oauth_access_tokens ADD CONSTRAINT oauth_access_tokens_client_id_fkey FOREIGN KEY (client_id) REFERENCES oauth_clients(id) ON DELETE CASCADE;
ALTER TABLE oauth_refresh_tokens DROP CONSTRAINT oauth_refresh_tokens_client_id_fkey;
ALTER TABLE oauth_refresh_tokens ADD CONSTRAINT oauth_refresh_tokens_client_id_fkey FOREIGN KEY (client_id) REFERENCES oauth_clients(id) ON DELETE CASCADE;
ALTER TABLE oauth_token_families DROP CONSTRAINT oauth_token_families_client_id_fkey;
ALTER TABLE oauth_token_families ADD CONSTRAINT oauth_token_families_client_id_fkey FOREIGN KEY (client_id) REFERENCES oauth_clients(id) ON DELETE CASCADE;


DROP INDEX oauth_refresh_tokens_client_id_idx;
DROP INDEX oauth_access_tokens_client_id_idx;
DROP INDEX oauth_authorization_codes_client_id_idx;
DROP INDEX oauth_clients_created_at_idx;
