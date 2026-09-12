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

Dynamic OAuth registration accepts one JSON object of at most 32 KiB,
including trailing whitespace. Client names are limited to 256 bytes;
`redirect_uris` accepts at most ten URIs, each at most 2,048 bytes. Oversized
bodies return 413; invalid fields return OAuth metadata errors with 400.
Unknown metadata extensions remain accepted.

`OAUTH_REGISTRATION_PER_IP` defaults to five attempts per minute, burst five.
`OAUTH_REGISTRATION_GLOBAL` defaults to 30 attempts per minute, burst ten.
Malformed attempts consume allowance too; throttling runs before parsing or
database access. Both limits return 429 and `Retry-After`.

By default, IP throttling uses the direct connection address. Behind local
Caddy, set `TRUSTED_PROXY_CIDRS=127.0.0.1/32,::1/128` and restrict direct access
to the backend. Only listed proxies may supply `X-Forwarded-For`; the first
untrusted address scanning from the right identifies the client. Malformed
chains fall back to the connection address. Include any additional proxies
only when they sanitize or append the real peer address. Without this setting,
clients behind Caddy intentionally share its IP allowance.

`OAUTH_MAX_CLIENTS` defaults to 10,000 total persisted clients. Both adapters
check capacity and insert within one serialized transaction. The quota
survives restarts and applies across processes sharing the same database;
configure the same quota on each. Capacity exhaustion returns 503 with an
OAuth `temporarily_unavailable` error and `Retry-After: 3600`.

At startup and hourly, maintenance removes up to 1,000 registrations older
than `OAUTH_UNUSED_CLIENT_HOURS` (default 24) with no authorization codes,
access tokens, refresh tokens, or token families. Even expired authorization
history is preserved; cleanup never clears revocation state. A registration
removed before consent completes must be registered again. Cleanup runs with
a 30-second deadline and is serialized against issuance. Failures are logged
and retried on the next hourly pass. Large backlogs take multiple passes.

Apply PostgreSQL migration 015 before deployment; jed applies migration 003
automatically on open. These migrations add indexes and make client foreign keys reject deletion
while grant history exists. Existing clients and tokens are preserved. Existing installations over quota keep their clients and
reject new registrations until cleanup or operator action frees capacity.
