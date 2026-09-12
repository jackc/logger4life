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

## Remaining findings, in priority order

### 1. Fix refresh rotation and family revocation atomically

**High priority; reproduced on the jed adapter.**
`RefreshOAuthToken` in `backend/core/oauth.go` calls `ConsumeRefreshToken`
and `issueTokenPair` separately. Both stores commit consumption before
replacement issuance. This permits the following interleaving:

1. Request A consumes refresh token R and pauses before creating its replacement.
2. Request B replays R. The store detects reuse and revokes the family's existing rows.
3. A resumes and inserts a fresh, valid pair into that same family.

A deterministic probe using the real jed store confirmed that the new access
token is accepted after reuse detection. The PostgreSQL implementation has
the same transaction boundary; its concurrent interleaving was identified
by inspection rather than reproduced against PostgreSQL. Existing tests
cover reuse after the replacement is already persisted.

Move rotation and issuance into one store operation with appropriate
locking, or persist a family revocation state checked during issuance and
authentication. Preserve committed revocation when returning the expected
reuse error. Simply wrapping the current action in a transaction would
roll that revocation back on an error unless error handling also changes.
Add a coordinated concurrency test to the shared store suite.

### 2. Bound request rate, concurrency, and registration growth

**High priority for the public endpoint.** The application and supplied
Caddy template do not rate-limit MCP tool calls or unauthenticated dynamic
registration. `/oauth/register` also decodes an unbounded body and accepts
unbounded client names and redirect lists. A caller can accumulate client
rows without authentication; an authorized caller can run many expensive
queries simultaneously.

The SDK's new 4 MiB MCP request cap and the SQL executor's existing
1000-row, 1 MiB value, and execution-time limits are useful per-request
limits, but do not bound aggregate work. Add per-user query concurrency and
rate limits, plus registration body/field limits, throttling, and retention.
Choose limits for this deployment and ensure proxy-based throttling uses
trusted client addresses. Tool invocation rate limiting is explicitly
required by the [tools specification](https://modelcontextprotocol.io/specification/2026-07-28/server/tools#security-considerations).

### 3. Enforce the granted scope at the resource boundary

**Medium priority; existing protocol gap.** Authorization validates each
element from `strings.Fields(scope)`, so a whitespace-only scope passes
without granting `mcp`. `AuthenticateOAuthToken` checks the token and
audience but never checks `grant.Scope`; that token still authorizes all
five tools. Normalize or reject empty explicit scope sets and require
`mcp` during authentication. A valid token lacking permission should receive
403 with `error="insufficient_scope"` and the required scope, rather than
being treated as an invalid token. This also establishes the boundary
needed before adding narrower scopes or write tools.

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

### 5. Tighten OAuth URL and consent boundaries

**Medium priority; existing hardening work.** `ValidRedirectURI` accepts
HTTPS strings with no hostname, including `https:callback`, and omits IPv6
loopback support. `SameCanonicalURL` ignores case across the entire URI,
including case-sensitive paths. Validate absolute redirect URIs explicitly;
normalize only scheme/host for resource comparisons. Keep the RFC 9207
issuer value exact. Validate that `MCP_CANONICAL_URL` is the documented
public origin at startup rather than accepting arbitrary paths or queries.

The consent page still relies on SameSite=Lax cookies, without an explicit
CSRF mechanism or framing restrictions. SameSite does not distinguish an
untrusted sibling origin on the same site. Add an origin/Fetch Metadata
check or session-bound CSRF token to consent submission and deny framing
of the consent page. The decision-injection fix above addresses a separate,
confirmed bug.

### 6. Bound large tool results and improve metadata caching

**Optional scalability work.** `list_logs` and `list_saved_queries` return
the complete collection. The SDK's `tools/list` pagination only paginates
tool definitions; it does not paginate records returned by these tools.
Add explicit cursor/limit arguments and output-size limits if collections
can grow substantially. Dedicated MCP DTOs could omit UI placement fields
and the unused `share_token` schema property; neither store currently
selects share tokens for `ListLogs`.

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
- The refresh-token interleaving probe reproduced the remaining finding;
  the passing race-detector run checks Go data races, not this transactional
  ordering bug.

Live external connector interoperability and full upstream conformance
certification were not run.
