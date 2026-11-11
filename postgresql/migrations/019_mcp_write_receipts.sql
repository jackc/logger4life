CREATE TABLE mcp_write_receipts (
 user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 operation TEXT NOT NULL,
 key TEXT NOT NULL,
 request_hash TEXT NOT NULL,
 result TEXT NOT NULL,
 PRIMARY KEY(user_id,operation,key)
);
GRANT ALL ON TABLE mcp_write_receipts TO logger4life;

---- create above / drop below ----
DROP TABLE mcp_write_receipts;
