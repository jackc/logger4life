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

## Where new code goes

- Pure validation, calculation, and domain types: `backend/domain`.
- Load/compute/save orchestration and action definitions: `backend/core`.
- SQL and persistence mechanics: `backend/pgstore`.
- HTTP cookies, status codes, request paths, and response writing: the HTTP
  adapter (currently the root `backend` package).

The `list_logs` path is the initial complete vertical slice. Existing paths
are migrated incrementally without changing their public API.
