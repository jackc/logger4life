# Resource limits

MCP accepts at most `MCP_REQUESTS_PER_MINUTE` authenticated POSTs per user
(default 60), with a `MCP_REQUEST_BURST` of 10. Initialization and tool-list
POSTs count too, ensuring older protocol clients have the same protection.
Discovery GETs remain available. Rejections return HTTP 429 and `Retry-After`.
Limits use the authenticated user ID across that user's tokens and clients.

These counters are process-local and reset on restart. Run one application
instance for these deployment-wide limits, or coordinate admission through a
shared limiter before adding replicas. Each tracking map holds at most 10,000
keys; idle entries expire after at least ten minutes (and a full refill).
When full, the map rejects new keys rather than resetting active allowances.

Limit environment variables must be positive integers, at most 1,000,000;
invalid values prevent startup.

SQL execution admits at most `SQL_CONCURRENCY_PER_USER` queries per user
(default 2) and `SQL_CONCURRENCY_GLOBAL` queries per application process
(default 8). These limits cover both MCP SQL tools and the web SQL API.
Requests are rejected immediately rather than queued. The API returns 429
with `Retry-After: 1`; MCP returns a tool error asking the caller to retry.
A slot is released only when the executor returns, including on cancellation
or error. Size the global limit for the database's capacity and connection
pool, leaving capacity for ordinary application operations.
