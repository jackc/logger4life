# MCP implementation review

Reviewed on 2026-09-12 against the stable **2026-07-28** specification.
The dependency is updated from Go SDK **v1.6.1 to v1.7.0**, the latest stable
release at review time. v1.8.0 is still in prerelease. v1.7.0 implements the
new revision while preserving older protocols. [SDK release notes](https://github.com/modelcontextprotocol/go-sdk/releases/tag/v1.7.0)

## Implemented in this update

| Area | Change and reason |
| --- | --- |
| HTTP lifecycle | Enable `Stateless`. Updating the dependency alone leaves the endpoint unable to serve the new protocol. The SDK now handles discovery, request metadata, and legacy initialization on the same endpoint. |
| Responses and cancellation | Use JSON responses for the five synchronous tools. Enable `PropagateRequestCancellation` so abandoned modern HTTP calls cancel their database work. |
| Capabilities | Stop advertising tool-list changes: this catalog is fixed at startup, so a persistent subscription is unnecessary. |
| Tool descriptions | Add display titles and explicit read-only, non-destructive, idempotent, closed-world annotations. The database restrictions enforce these properties independently of the hints. |
| Browser origins | Validate every supplied `Origin` against the configured public origin, rejecting mismatches with 403. Keep the localhost Host-check exception required by the Caddy reverse proxy. |
| OAuth challenges | Include `scope="mcp"`, accept case-insensitive Bearer schemes, and omit `invalid_token` on an initial request without bearer credentials. |
| OAuth issuer binding | Advertise RFC 9207 support and include `iss` in successful, denied, and redirectable error responses. |
| Token caching | Set `Cache-Control: no-store` and `Pragma: no-cache` on issued and refreshed tokens. |
| Consent correctness | Remove supplied `approve` fields from hidden form inputs, read POST decisions only from the POST body, and reject ambiguous decisions by rendering consent again. Previously `?approve=true` could become a hidden input preceding the Deny button, causing Deny to issue a code. |

The transport changes follow the current [Streamable HTTP requirements](https://modelcontextprotocol.io/specification/2026-07-28/basic/transports/streamable-http).
Scope guidance and issuer responses follow the current [authorization specification](https://modelcontextprotocol.io/specification/2026-07-28/basic/authorization).

## Review findings and follow-up

### 1. Refresh-token family revocation — resolved

The original high-priority finding was reproduced on the jed adapter.
`RefreshOAuthToken` calls `ConsumeRefreshToken` and `issueTokenPair`
separately. Previously, revocation affected only existing token rows, which
permitted the following interleaving:

1. Request A consumes refresh token R and pauses before creating its replacement.
2. Request B replays R. The store detects reuse and revokes the family's existing rows.
3. A resumes and inserts a fresh, valid pair into that same family.

Both adapters now persist revocation in `oauth_token_families` and check it
during issuance and authentication. PostgreSQL locks the family row before
touching token rows; jed's write transaction provides serialization. Reuse
of an ancestor and issuance of a descendant therefore share the same
revocation boundary. Explicit refresh-token revocation also revokes the
family, including replacements that are still pending.

Issuance into a revoked family returns the same generic OAuth
`invalid_grant` response as reuse during consumption. Revocation commits
before the expected error is returned. Shared regression tests cover the
three interleavings, while concurrent tests exercise each real adapter
separately. Migration tests preserve existing tokens and check revocation
across reopening the embedded database.

PostgreSQL migration 014 must run before deploying the updated server.
Embedded databases apply migration 002 automatically on open. Existing
families are backfilled without requiring users to reconnect.

### 2. Bound request rate, concurrency, and registration growth — resolved

MCP now limits authenticated POSTs by user ID (60/minute, burst ten).
Shared SQL execution admits two queries per user and eight globally per
process, rejecting excess work without queueing. Slots remain held until
execution actually stops, including after cancellation.

Registration accepts one JSON document of at most 32 KiB, names of at most
256 bytes, and up to ten redirect URIs of at most 2,048 bytes each. Per-IP
and global token buckets throttle attempts before parsing or database work.
Only explicitly configured proxy CIDRs can supply forwarded client IPs.
Limiter maps are bounded and evict idle entries.

Both database adapters atomically enforce a configurable total client quota
(default 10,000). Startup and hourly cleanup remove up to 1,000 registrations
older than 24 hours that have no authorization records. Existing codes,
tokens, and token-family history protect their client from deletion; cleanup
is serialized against issuance. PostgreSQL migration 015 and automatic jed
migration 003 add cleanup indexes and restrict deletion of clients referenced
by grant history.

See [resource limits](resource-limits.md) for configuration and responses.
Traffic and concurrency counters are process-local; coordinate admission
before running replicas. The client storage quota is database-enforced.
Concurrent regression tests exercise each real database independently.

### 3. Enforce the granted scope at the resource boundary — resolved

Authorization rejects whitespace-only scope sets with `invalid_scope` before
consent or code issuance. An omitted or empty scope still defaults to `mcp`;
accepted scope sets have their whitespace normalized before storage.

`AuthenticateOAuthToken` now requires an exact, case-sensitive `mcp` member
in the stored grant after validating the token and audience. This also denies
previously issued tokens with empty or unrelated scopes. Every MCP request
passes through this boundary before reaching the resource handler.

Valid tokens lacking permission receive 403 with `error="insufficient_scope"`,
`scope="mcp"`, and the protected-resource metadata URL in `WWW-Authenticate`.
Missing, invalid, expired, revoked, and wrong-audience tokens retain their 401
behavior. No database migration is needed; affected clients must authorize
again with `mcp`.

Regressions cover authorization preview and approval, normalized scope in the
token response, exact scope membership, denial before resource dispatch even
with a browser session, and the distinction between 401 and 403 challenges.

### 4. Add Client ID Metadata Documents

**Recommended protocol modernization.** Registration currently supports
only database-backed DCR clients. The new spec recommends Client ID
Metadata Documents (CIMD), and deprecates DCR while retaining it for
compatibility. Keep DCR while introducing HTTPS URL client IDs, metadata
validation, redirect binding, bounded fetching, SSRF protection, and HTTP
cache handling. Advertise `client_id_metadata_document_supported` only
after the implementation exists. Client IDs are already stored as text,
but grants reference persisted client records; define how fetched clients
and metadata refreshes fit that lifecycle.

Ignoring `application_type` is acceptable here because this authorization
server does not implement OIDC; the new requirement primarily concerns
clients and OIDC registration constraints. [Client registration requirements](https://modelcontextprotocol.io/specification/2026-07-28/basic/authorization/client-registration)

### 5. Tighten OAuth URL and consent boundaries — resolved

**URL validation — resolved.** Redirect URIs must be absolute hierarchical
HTTPS URLs or HTTP loopback URLs with a hostname and valid port. Credentials,
fragments (including an empty `#`), and malformed authorities are rejected.
IPv6 loopback callbacks are supported. Authorization also revalidates stored
callbacks so malformed registrations created before the fix cannot issue
new codes or redirect errors. Callback matching remains exact.

Resource comparisons normalize scheme/host, default ports, and an empty
root path, while preserving case-sensitive escaped paths and query strings.
They no longer equate `/Resource` with `/resource` or `/` with `///`.
`MCP_CANONICAL_URL` is validated before opening a database and must be an
HTTPS origin (HTTP is allowed only on loopback for local development).
Credentials, non-root paths, queries, and fragments are rejected. A single
root slash, scheme/host case, and default ports are normalized once into
the public origin used by discovery, consent checks, and core OAuth actions.
The RFC 9207 `iss` value is emitted exactly as advertised; it never uses the
resource comparison helper.

Regressions cover URL parsing, IPv4/IPv6 loopback, old malformed client
records, exact callback binding, rejection before database startup, and
resource mismatches at consent, code issuance/exchange, refresh, and bearer
authentication. The backend suite passes for PostgreSQL, jed, and the dual
adapter. Chromium also verifies normalized startup configuration, exact
issuer responses, and an approved MCP tool call. Focused OAuth/URL tests
also pass under the Go race detector. No database migration is needed.
Deployments with invalid canonical URLs must correct their configuration;
clients with malformed callbacks must register again with valid URLs.

**Consent protection — resolved.** A Chromium reproduction confirmed that
an untrusted sibling origin could submit approval using the user's
SameSite=Lax session and obtain working access and refresh tokens without
displaying consent. Secure and HttpOnly cookie attributes did not prevent
this attack. The sibling origin could also frame the consent page.

Authorization POSTs now require exactly one `Origin` header matching the
configured public origin, independently of proxy Host or forwarded headers.
Missing, null, duplicate, and foreign origins fail with an inline 403 before
authorization processing. Initial cross-origin GET navigation remains
supported. All authorization responses deny framing with CSP
`frame-ancestors 'none'` and `X-Frame-Options: DENY`. Automated consent
submissions must send the canonical Origin, just as a browser does.
Regressions cover approval and denial, forged provenance, proxy hosts,
framing headers, and failure before code issuance. The decision-injection
fix above addresses a separate bug. Chromium verification confirms the
forged POSTs and framing are blocked while explicit approval still yields
a working token and a successful MCP tool call.

### 6. Bound large tool results — resolved; metadata caching remains optional

`list_logs` and `list_saved_queries` now accept cursor/limit arguments, default
to 50 records, and cap requests at 100. Both adapters fetch a bounded page
using a stable lowercase-name/ID key and the authenticated user's visibility
rules. Core enforces a five-second database deadline and a 256 KiB page budget,
including the continuation cursor. The SDK's duplicated text and structured
representation stays below 1 MiB. A record that cannot fit returns a tool
error; shortened pages resume after the last returned row. Dedicated log
summaries omit UI placement and share-token properties.

Regression tests cover equal-name page boundaries, scoped cursors, revoked
sharing, byte-driven truncation, malformed input, and the serialized MCP
response and schemas. The web UI's collection endpoints keep their existing
response contract.

The fixed tool catalog can use a positive cache TTL. The SDK currently
emits `cacheScope="public"` and `ttlMs=0`; this is appropriate for the
identical definitions all users receive, but offers no freshness interval.
User data is returned by tool calls, not this catalog. If tool availability
ever varies by authorization, revisit its cache policy before caching.

## Changes already handled by the SDK or not currently needed

- Discovery, per-request protocol metadata, `resultType`, header/body
  consistency checks, deterministic tool ordering, and cache fields are
  SDK responsibilities. HTTP tests exercise them through this application's
  configured handler. The [specification changelog](https://modelcontextprotocol.io/specification/2026-07-28/changelog)
  describes these changes.
- Typed tool inputs and outputs remain useful. Existing object schemas
  need no conversion merely because the new spec permits additional JSON
  Schema shapes. The SDK returns both structured data and JSON text and
  converts handler validation failures to tool errors.
- Protected-resource discovery at the root and the explicit metadata URL
  in challenges are valid; a second `/.well-known/oauth-protected-resource/mcp`
  endpoint is optional. The configured origin is a valid resource
  identifier for this single-resource deployment. [Discovery requirements](https://modelcontextprotocol.io/specification/2026-07-28/basic/authorization/authorization-server-discovery)
- Tool handlers already propagate the authenticated user into shared core
  actions. SQL adapters enforce tenant filtering, read-only execution,
  permitted functions, and result limits. Expected errors are sanitized;
  infrastructure errors are logged and replaced.
- Multi Round-Trip Requests, elicitation, Tasks, Apps, resources, and prompts
  need a concrete product use case. None is required for these synchronous
  read tools. Roots, sampling, and protocol logging are deprecated; do not
  add them merely to increase feature coverage.

## Validation

- Resource-limit regressions cover bursts and refill, bounded limiter state,
  shared SQL concurrency and cancellation, registration input and IP trust,
  concurrent client quotas and retention, and database-backed list pagination.
- `mise run test:backend`: passed, including PostgreSQL, jed, and dual-store
  server suites.
- Affected MCP/OAuth tests with `go test -race`: passed.
- HTTP coverage includes protocols 2025-06-18, 2025-11-25, and 2026-07-28;
  discovery, schemas and annotations, error responses, header mismatches,
  per-request user identity, cookies not substituting for bearer auth,
  invalid origins, legacy initialization without retained sessions, and
  request cancellation reaching the SQL executor.
- OAuth integration coverage includes a real modern tool call, issuer
  responses, token cache headers, and the consent decision regression.
- The original refresh-token probe reproduced the finding. Follow-up
  regressions now exercise pending issuance after current-token replay,
  ancestor replay, and explicit revocation. Concurrent revocation and
  migration tests run on both storage adapters; the Go race detector
  complements these transaction-level checks.

Live external connector interoperability and full upstream conformance
certification were not run.
