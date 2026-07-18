# TODO: Complete the core architecture refactor

Temporary working plan for finishing the migration to the domain core, store
ports, and action catalog architecture modeled after `.scratch/fam`.

## Completed

- [x] Add `backend/core` with typed actions, catalog lookup, JSON invocation,
  validation, middleware support, and trusted user context.
- [x] Add `backend/domain` for pure business types and validation.
- [x] Add `backend/pgstore` for PostgreSQL implementations of core ports.
- [x] Migrate log create/get/list/update/delete operations.
- [x] Migrate folder create/list/rename/move/delete operations.
- [x] Migrate saved-query create/get/list/update/delete operations.
- [x] Migrate SQL schema discovery.
- [x] Route migrated HTTP and MCP operations through the same actions.
- [x] Document the dependency rules in `docs/architecture.md`.

## Remaining feature migrations

### 1. Log entries and placement

- [x] Define domain types for log entries and placement changes.
- [x] Add actions for creating, listing, updating, and deleting log entries.
- [x] Keep field-value validation in `backend/domain`.
- [x] Add actions for folder placement, position changes, home pinning, and
  home ordering.
- [x] Move entry and placement SQL and transactions into `backend/pgstore`.
- [x] Remove the legacy folder-ownership helper from the HTTP package.

### 2. Sharing

- [x] Add store ports and actions for creating and deleting share tokens.
- [x] Add actions for listing and removing shared users.
- [x] Add actions for reading share information and joining a shared log.
- [x] Move access checks and membership transactions out of HTTP handlers.
- [x] Represent expected sharing failures as core sentinel errors.

### 3. Authentication and sessions

- [x] Define core user and session types without exposing password hashes.
- [x] Add user and session store ports.
- [x] Add register, password-login, session-authentication, logout, profile,
  email-change, and password-change actions.
- [x] Move password policy and credential orchestration into core actions.
- [x] Keep cookie parsing and writing in the HTTP adapter.
- [x] Move session database access out of authentication middleware.
- [x] Add a transactor port for multi-write registration/session actions.

### 4. Passkeys

- [x] Define passkey and challenge store ports.
- [x] Add begin/finish registration and login actions.
- [x] Add list, rename, and delete passkey actions.
- [x] Keep WebAuthn HTTP request/response translation in the adapter.
- [x] Move credential and challenge persistence into store implementations.

### 5. User SQL execution

- [ ] Define query parameter/result types in core or a pure SQL-query domain
  package.
- [ ] Add a driven port for executing constrained user queries.
- [ ] Add an action for SQL execution shared by HTTP and MCP.
- [ ] Keep arbiter enforcement and PostgreSQL execution in infrastructure.
- [ ] Translate safe query failures into core errors without leaking database
  details.

### 6. OAuth

- [ ] Decide and document whether OAuth protocol storage is infrastructure
  exempt from the action catalog or part of the catalog boundary.
- [ ] If included, define OAuth store ports and actions for client, code,
  token, consent, and revocation operations.
- [ ] Keep OAuth protocol HTTP translation in the server adapter.
- [ ] Move remaining OAuth SQL out of the root backend package.

## Architectural cleanup

- [ ] Move the HTTP adapter from the root `backend` package into
  `backend/server`, matching the reference layout.
- [ ] Remove compatibility SQL helpers after their tests use core/store APIs:
  `listSQLSchemaViews`, saved-query helpers, and folder ownership helpers.
- [ ] Ensure the composition root constructs one store and one `core.Core` and
  injects them into every adapter.
- [ ] Add transaction middleware/ports for actions spanning multiple writes.
- [ ] Add authorization and audit middleware where appropriate.
- [ ] Decide whether health and hello database checks remain adapter-level
  infrastructure operations.
- [ ] Add an architectural test or linter that rejects SQL and PostgreSQL
  imports outside approved infrastructure packages.

## Testing

- [ ] Add core unit tests using narrow fake ports for each action group.
- [ ] Add shared store conformance tests for each persistence port.
- [ ] Preserve the existing HTTP and browser tests as adapter and end-to-end
  coverage.
- [ ] Test sentinel error translation independently from PostgreSQL errors.
- [ ] Test middleware execution for typed and dynamic action invocation.
- [ ] Run `go test ./...` and `git diff --check` after every migration slice.

## Completion criteria

- [ ] Every operation that reads or changes persistent application state is a
  catalog action, except explicitly documented infrastructure exemptions.
- [ ] HTTP and MCP adapters contain no business rules or application SQL.
- [ ] Pure calculations and validation live in domain packages.
- [ ] All effects are represented by core-owned driven-port interfaces.
- [ ] PostgreSQL-specific types and errors do not escape `backend/pgstore` or
  another explicitly designated infrastructure package.
- [ ] The action catalog is the authoritative inventory of application
  capabilities.
- [ ] The full Go and browser test suites pass.
