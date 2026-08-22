# Architecture

Logger4Life is being organized around a domain core, driven stores, and a
reified action catalog. The pattern follows `.scratch/fam`.

## Dependency direction

`backend/domain` contains pure types and rules. It performs no I/O and does
not depend on the service or adapter packages.

`backend/core` is the application service layer. Every operation that reads
or changes persistent state is an action declared with `core.Define`. Actions
contain orchestration, accept JSON-tagged parameter structs, and call driven
port interfaces owned by this package. `core.Catalog` is the complete,
enumerable catalog; typed callers use `Action.Call`, while dynamic adapters
use `Core.InvokeJSON`.

`backend/pgstore` implements core's persistence ports. PostgreSQL details and
error translation remain here and do not leak into domain rules.

HTTP, MCP, CLI, and future background workers are driving adapters. They
authenticate and translate transport data, put trusted identity into the
core context, invoke an action, and translate its result. They do not own
business rules or SQL.

Authentication follows the same boundary. `core.User` is the serializable
identity and never contains a password hash; hashes only cross `UserStore`
method boundaries. Core actions own password policy, bcrypt orchestration,
session token generation, and registration's user/session transaction. The
HTTP adapter owns cookie parsing and attributes, while `pgstore` owns all user
and session SQL.

Passkeys also cross this boundary through catalog actions. Core owns WebAuthn
ceremony orchestration, challenge lifetime and one-time consumption, credential
updates, and the credential/session login transaction. `PasskeyStore` and
`PasskeyChallengeStore` keep all credential and challenge persistence in
`pgstore`. The HTTP adapter only translates WebAuthn JSON, trusted user context,
session cookies, and status codes.

User-authored SQL is represented by the `execute_user_sql` catalog action and
the `UserSQLExecutor` driven port. Core owns authentication and input limits;
`pgstore` owns the arbiter, restricted-role read-only transaction, per-user view
context, PostgreSQL execution, timeouts, and row/result-size limits. Only typed,
curated query failures may cross back to HTTP or MCP. Raw parser, database,
pool, and network errors remain infrastructure details.

OAuth 2.1 is part of the catalog boundary, not an infrastructure exemption.
Clients, authorization codes, and token families are persistent application
state that gates access to user data, and the rules protecting them — PKCE
verification, exact redirect-URI matching, single-use codes, RFC 8707
audience binding, and refresh-token family revocation on reuse — are business
rules rather than transport translation. They therefore live in core actions
with an `OAuthStore` driven port, alongside sessions and passkeys. `pgstore`
owns the atomic consume-and-invalidate statements that make replay and reuse
detection work. The HTTP adapter keeps only OAuth protocol translation:
metadata documents, form parsing, the consent page, redirects, bearer
challenges, and the mapping of `core.OAuthError` onto RFC 6749 error
responses. Plaintext tokens never reach a store; core hashes them first, and
`OAuthError` carries only descriptions that are safe to return to a client.

`backend/server` is the composition root. `server.Run` opens the pool, builds
one `pgstore.Store` and one `core.Core`, and injects that core into every HTTP
and MCP adapter; no handler constructs its own. Health and hello are
infrastructure liveness probes rather than catalog operations — they read no
application state — so `Run` supplies a `HealthCheck` function and the handlers
hold neither SQL nor a connection pool.

Transaction boundaries are declared by actions, not imposed on them. An action
that spans several writes wraps them in `c.tx.InTx`; every `pgstore` method
reaches its connection through `conn(ctx)` or `InTx`, so calls made with that
context join the transaction rather than opening their own. There is no
automatic per-action transaction middleware, which keeps reads and single-write
actions out of transactions they do not need.
`TestStoreHonorsAmbientTransaction` covers the guarantee the ports must keep.

`TestArchitecturalBoundaries` in the root `backend` package enforces these
rules mechanically. It walks every Go file and fails when one imports outside
its layer: PostgreSQL packages are confined to `backend/pgstore` and the
composition root, and `backend/core` and `backend/domain` may not import
transport or persistence at all. SQL cannot be executed without a PostgreSQL
import, so restricting the import is what keeps application SQL inside
infrastructure.

## Where new code goes

- Pure validation, calculation, and domain types: `backend/domain`.
- Load/compute/save orchestration and action definitions: `backend/core`.
- SQL and persistence mechanics: `backend/pgstore`.
- HTTP cookies, status codes, request paths, and response writing:
  `backend/server`.
- Cobra commands and flag plumbing: the root `backend` package, which owns the
  CLI and calls `server.Run`.

The `list_logs` path is the initial complete vertical slice. Existing paths
are migrated incrementally without changing their public API.
