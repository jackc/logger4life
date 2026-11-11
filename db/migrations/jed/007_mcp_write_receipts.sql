CREATE TABLE mcp_write_receipts (
 user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 operation TEXT NOT NULL,
 key TEXT NOT NULL,
 request_hash TEXT NOT NULL,
 result TEXT NOT NULL,
 PRIMARY KEY(user_id,operation,key)
);

---- create above / drop below ----
DROP TABLE mcp_write_receipts;
