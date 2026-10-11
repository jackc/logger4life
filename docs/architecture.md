# Architecture

Logger4Life is being organized around a domain core, driven stores, and a
reified action catalog. The pattern follows `.scratch/fam`.

## Dependency direction

`action` supplies the reusable catalog and dispatcher. Registries are explicit
instances parameterized by application dependencies and metadata. Dispatchers
bind a registry to middleware and handle typed calls, JSON decoding, and
parameter validation. Dependencies are supplied per invocation; a dispatcher
does not retain its application instance. Typed and dynamic invocations require
the action to belong to the dispatcher's registry. Middleware sees only the
action's `Info[M]` (name, description, metadata) and invocation parameters,
without a dependency type or invocation method. The package does not interpret
metadata or depend on any backend package.

`InvokeJSON` uses JSON v2 to accept exactly one JSON value and reject unknown fields,
duplicate names, invalid Unicode, and trailing data before entering middleware;
trailing whitespace is allowed. Field names are case-sensitive. Action and HTTP
registry package tests enforce independence from other repository packages.

`backend/domain` contains pure types and rules. It performs no I/O and does
not depend on the service or adapter packages.

`backend/core` is the application service layer. Every operation that reads
or changes persistent state is an action declared with `core.Define`. Actions
contain orchestration, accept JSON-tagged parameter structs, and call driven
port interfaces owned by this package. `core.Catalog` is the complete,
enumerable catalog; typed callers use `Action.Call`, while dynamic adapters
use `Core.InvokeJSON`.

Core owns Logger4Life's registry and the `ActionMetadata` fields `Public` and
`Mutating`. Its small action wrapper preserves the application's declarations
and typed call sites while delegating execution to `action`. Authentication
and mutation auditing remain application middleware, with their existing
behavior and configuration.

`backend/pgstore` and `backend/jedstore` each implement the same persistence
ports. PostgreSQL and embedded-database details, SQL differences, and error
translation remain in their adapter and do not leak into domain rules.
`backend/dualstore` composes those adapters for validation: it invokes both,
returns PostgreSQL's result, and fails immediately on an observable divergence.

HTTP, MCP, CLI, and future background workers are driving adapters. They
authenticate and translate transport data, put trusted identity into the
core context, invoke an action, and translate its result. They do not own
business rules or SQL.

Authentication follows the same boundary. `core.User` is the serializable
identity and never contains a password hash; hashes only cross `UserStore`
method boundaries. Core actions own password policy, bcrypt orchestration,
session token generation, and registration's user/session transaction. The
HTTP adapter owns cookie parsing and attributes, while each persistence adapter
owns its user and session SQL.

Passkeys also cross this boundary through catalog actions. Core owns WebAuthn
ceremony orchestration, challenge lifetime and one-time consumption, credential
updates, and the credential/session login transaction. `PasskeyStore` and
`PasskeyChallengeStore` keep credential and challenge persistence behind the
same ports in both stores. The HTTP adapter only translates WebAuthn JSON,
trusted user context, session cookies, and status codes.

User-authored SQL is represented by the `execute_user_sql` catalog action and
the `UserSQLExecutor` driven port. Core owns authentication and input limits;
each store owns the arbiter, an isolated read-only execution context, per-user
row filtering, timeouts, and row/result-size limits. PostgreSQL uses restricted
views and a transaction; jed materializes the same projections as session-local
temporary tables with SELECT-only privileges. Only typed, curated query
failures may cross back to HTTP or MCP. Raw parser, database, pool, and network
errors remain infrastructure details.

OAuth 2.1 is part of the catalog boundary, not an infrastructure exemption.
Clients, authorization codes, and token families are persistent application
state that gates access to user data, and the rules protecting them — PKCE
verification, exact redirect-URI matching, single-use codes, RFC 8707
audience binding, and refresh-token family revocation on reuse — are business
rules rather than transport translation. They therefore live in core actions
with an `OAuthStore` driven port, alongside sessions and passkeys. Each store
owns the atomic consume-and-invalidate operations that make replay and reuse
detection work. The HTTP adapter keeps only OAuth protocol translation:
metadata documents, form parsing, the consent page, redirects, bearer
challenges, and the mapping of `core.OAuthError` onto RFC 6749 error
responses. Plaintext tokens never reach a store; core hashes them first, and
`OAuthError` carries only descriptions that are safe to return to a client.

`backend/server` is the composition root. `server.BuildBackend` selects
PostgreSQL, jed, or the fail-stop `both` comparison adapter, builds one store
and one `core.Core`, and `server.Run`
injects that core into every HTTP and MCP adapter; no handler constructs its
own. Health and hello are
infrastructure liveness probes rather than catalog operations — they read no
application state — so `Run` supplies a `HealthCheck` function and the handlers
hold neither SQL nor a connection pool. `GET /api/settings` is exempt for the
same reason: it reports what this process was configured with, so there is no
stored state for an action to own.

Every other HTTP handler and every MCP tool reaches persistent state only by
calling a catalog action, which is what makes the catalog the inventory of
what this application can do rather than a partial view of it.

Authorization is declared by actions and enforced twice. An action states
whether it is `Public` — reachable before the caller is anyone — and the
`RequireUser` middleware the composition root installs turns an anonymous
caller away from everything else. The actions still establish the caller for
themselves, so the middleware is a second line rather than the only one: what
it adds is that a new action is closed by default, and that an anonymous
caller is refused before hearing anything about the parameters it sent, since
middleware wraps parameter validation rather than following it.

Auditing is middleware for the opposite reason: it is the one concern that has
to cover every action, and an action that forgot to record itself would be the
one worth having a record of. It logs mutating actions only — reads are the
bulk of traffic and the request log already covers them — and records the
action and the caller rather than the parameters, which carry the very data
the log exists to describe.

Transaction boundaries are declared by actions, not imposed on them. An action
that spans several writes wraps them in `c.tx.InTx`; both stores reach their
active connection or transaction through the context, so calls made with that
context join the transaction rather than opening their own. There is no
automatic per-action transaction middleware, which keeps reads and single-write
actions out of transactions they do not need.
`TestStoreHonorsAmbientTransaction` covers the guarantee the ports must keep.

`TestArchitecturalBoundaries` in the root `backend` package enforces these
rules mechanically. It walks every Go file and fails when one imports outside
its layer: database packages are confined to their persistence adapter and the
composition root, and `backend/core` and `backend/domain` may not import
transport or persistence at all.

## Action inventory

From the checkout root, run `go run ./cmd/action-inventory > actions.json`
(also available as `mise run inventory:actions`). The development command
exports a versioned JSON document with the core package path and an `actions`
array sorted by action name. Entries contain `name`, `symbol` (the Go variable),
`description`, and `source` with a repository-relative `file` and one-based
`line` pointing to the declaration's `Define` call.

The tool reads and type-checks source using the current Go build environment;
it does not import or execute application code or start services. Test files
are excluded. It recognizes direct package-level calls to this package's
`Define` function, including explicit type arguments. Names and descriptions
must be compile-time string constants. Recognized calls in unsupported forms
or duplicate names cause an error on stderr and a nonzero exit, with no JSON
written. No report file is maintained by hand.

This is an inventory of the current declaration convention, not a general Go
registration analysis: aliases of `Define`, other registration APIs, and
declarations in other packages are outside its scope. A test compares the
source inventory against the production runtime catalog to catch drift for
the tested build. The inventory contains no policy verification, transport
exposure, or handler analysis. Source locations refer to the checkout being
inspected. Use `-root /path/to/logger4life` to inspect another checkout.

## HTTP registry

`httpregistry` is a reusable, application-independent package for HTTP registration,
metadata compilation, and inspectable typed bindings. Chi remains private to its
routing implementation. Core and domain cannot import the registry; `action`
remains independent of transports. See [the library contract](../httpregistry/README.md).

`backend/server/http.go` is the single production route assembly, also used by the
HTTP integration tests. Every endpoint has application-owned `HTTPMetadata` with
`Public` and `Credentials`. A compiler turns those values into the session-user
or OAuth bearer gate; core action `Public` does not control HTTP exposure or
HTTP authentication. Global request ID, optional session loading, and logging
stages preserve their previous order, including for MCP and unmatched requests.
The effective chain appears in every inventory entry, without subrouter policy
inheritance. The MCP wrapper's origin, bearer, and POST rate-limit checks remain
one named compound stage with their existing behavior.

All routes, including health, settings, login, OAuth, and the MCP mount, register
through the library. Three routes use generated typed bindings: `GET /api/logs`,
`GET /api/logs/{logID}`, and `POST /api/logs/{logID}/entries`. Their mappings invoke
the existing `core.Action.Call` pipeline, preserve response shapes/statuses and
application error writers, and keep identity in trusted context. The mutation
uses strict `encoding/json/v2` request decoding: unknown fields, duplicate keys,
case aliases, invalid Unicode, top-level null, and trailing input are rejected.
Response encoding uses `encoding/json/v2`. No new body limit or content-type
requirement is applied to these existing endpoints.

All first-party JSON code, including tests, uses `encoding/json/v2` and
`encoding/json/jsontext`; a repository-wide import test prevents v1 imports.
Custom application handlers share an object-only decoder with unknown-field
rejection. OAuth registration permits metadata extensions, and WebAuthn
credentials remain raw JSON for the protocol library to interpret. Both still
reject malformed JSON, duplicate names, invalid Unicode, and trailing input.
Responses use v2's empty-array/empty-object defaults for nil collections;
optional scalar and pointer fields use `omitzero` to retain their omission rules.
Response encoding finishes before headers are committed so encoding errors can
return HTTP 500. Stored sessions, cursors, and jed JSON have v1 fixture coverage.
MCP page-size accounting explicitly matches the SDK's HTML and JavaScript
escaping, and inventory commands request deterministic, indented output.

The application's `bindHTTPAction` adapter supplies the core namespace and handles
transport input errors before delegating domain errors to each binding's writer.
`CustomFactory` defers OAuth/MCP construction until an enabled registration needs
it; all those registrations share one protocol server. Disabled routes remain in
the inventory without initializing their services. OAuth configuration is applied
equally to production and test-injected providers.

Run `go run ./cmd/http-inventory` or `mise run inventory:http` to export a
configuration-specific JSON inventory. Flags `-passkeys`, `-mcp-origin URL`, and
`-registration` describe a feature configuration; the command does not read
service configuration from the environment or open databases. The same handler
constructors assemble the description without executing requests or starting
background work. `server.HTTPInventory(cfg)` supports callers that already have
a configuration. The report includes nonsecret feature settings, disabled
passkey/OAuth/MCP declarations, explicit endpoint metadata, and binding plans.
Registration-disabled is a handler behavior: `/api/register` remains installed.

An entry's `gaps` marks opaque handler/protocol internals. Installed stages are
construction evidence, not verification of their implementation. Handler-local
limits, custom codecs/error writers, and transitive action calls are not inferred.
Authentication middleware may call actions even on endpoints with no direct
binding. Mounted protocol methods and internal dispatch remain opaque. HTTP
source locations and a combined HTTP/action source report are not implemented;
the existing action inventory and its recognition limits are unchanged.

## Where new code goes

- Generic catalog and dispatch mechanics: `action`.
- Generic HTTP registration, inventory, and typed binding mechanics: `httpregistry`.
- Pure validation, calculation, and domain types: `backend/domain`.
- Load/compute/save orchestration and action definitions: `backend/core`.
- PostgreSQL persistence mechanics: `backend/pgstore`.
- Embedded jed persistence mechanics: `backend/jedstore`; embedded migrations:
  `db/migrations/jed`.
- Cross-engine comparison harness: `backend/dualstore`.
- HTTP cookies, status codes, request paths, and response writing:
  `backend/server`.
- Cobra commands and flag plumbing: the root `backend` package, which owns the
  CLI and calls `server.Run`.

The `list_logs` path is the initial complete vertical slice. Existing paths
are migrated incrementally without changing their public API.
