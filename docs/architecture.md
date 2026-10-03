# Architecture

The running server is a native Rust binary defined by the root `Cargo.toml`.
The Svelte frontend remains in `src/`; Rust code lives in `rust/src/`. The Go
implementation under `backend/` is retained as a compatibility reference and
fixture generator, and is not linked or launched by the Rust server.

## Modules

| Module | Responsibility |
|---|---|
| `main.rs`, `config.rs` | CLI, environment/flag precedence, startup validation, logging |
| `server.rs` | HTTP adapter, cookie identity, routing, request IDs, audit events, graceful shutdown |
| `auth.rs` | bcrypt passwords, registration, sessions, profile changes |
| `catalog.rs`, `catalog/domain.rs` | Logs, entries, folders, placements, sharing, saved queries, validation and collection pagination |
| `passkeys.rs`, `passkeys/` | One-time WebAuthn ceremonies, existing COSE keys, conventional and ML-DSA verification, compatible attestation validators |
| `oauth.rs` | Public clients, consent, PKCE, code exchange, token rotation/revocation, cleanup |
| `cimd.rs` | Bounded HTTPS metadata fetching, public-address checks, DNS pinning, validation and caching |
| `mcp.rs` | Stateless JSON MCP transport over the same catalog and SQL services |
| `store.rs`, `store/user_sql.rs` | Native PostgreSQL/jed execution, transactions, comparison mode and isolated SQL |
| `cancellation.rs` | Request cancellation across the asynchronous HTTP and blocking database boundary |

`App` owns the validated configuration, database adapter, and request limits.
HTTP work enters the blocking pool before calling synchronous application
operations. Application operations use `Database::read`, `transaction`, or their
timeout variants; every query in a callback shares the same transaction. Failures
roll back the callback. Pure field/name/note validation performs no I/O.

Database SQL stays parameterized. The adapter normalizes UUIDs, timestamps,
JSON, arrays and binary data across PostgreSQL and native jed, and translates
expected constraint errors into stable public messages. Dialect-specific
queries are explicit where required. `both` executes operations on both engines
and stops the process on a mismatch, while normalizing documented differences
such as database-generated audit timestamps and SQL type aliases.

## Authentication and data isolation

Protected handlers check identity before decoding input. Cookie sessions and
OAuth bearer tokens are separate; a cookie cannot authorize MCP. Queries enforce
ownership or current sharing membership. A saved placement does not restore
access after a share is revoked. Password hashes and credential material are
excluded from public responses. Logs record request/action metadata without
request bodies, passwords or tokens.

OAuth codes and refresh tokens are consumed under transaction locks. Refresh
reuse revokes the persistent token family, including its active access tokens.
CIMD identifiers and registered redirect URIs retain their exact string identity.
Metadata fetching rejects private/special-use IP answers, mixed public/private
DNS answers, redirects, proxies, compression and duplicate security fields.

User SQL passes an AST-based statement, table and function policy before
execution. PostgreSQL uses a read-only transaction, a restricted role and the
existing per-user `sql_query` views. Jed materializes a snapshot of visible rows
into session-local tables and grants only SELECT on those tables. Queries have
concurrency limits, cancellation, a five-second deadline, and bounded row/byte
collection. HTTP and MCP call this same execution boundary.

## Compatibility and tests

All existing migrations remain authoritative: PostgreSQL uses tern; jed embeds
and automatically applies the unchanged migrations. Tests cover Go-created jed
files, native persistence, HTTP/protocol contracts, OAuth replay and portable
COSE credentials. The existing Playwright tests run against the Rust server.
Use `LOGGER4LIFE_TEST_BACKEND=jed` with a disposable `JED_DATA_DIR` to run the
browser tests against the embedded backend.

Go source and tests can be removed after the native compatibility coverage is
sufficient for the project's migration policy. Until then, keep them available
as an independent behavior reference.
