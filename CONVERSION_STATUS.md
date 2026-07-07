# Database Conversion Status: jed ⇄ PostgreSQL

Goal: make Logger4Life run on **either** [jed](https://github.com/jackc/jed) (embedded,
zero-dependency, easy install) **or** PostgreSQL (traditional, larger-scale deploy), selectable at
deploy time.

This is the first project to adopt jed, so this doc doubles as a **running log of jed
infelicities** worth feeding back into jed. See [Infelicities](#infelicities-found-in-jed).

Status: **🟢 Phase 1 done — DB seam + PostgreSQL backend.** The codebase now talks to the database
through a backend-agnostic `DB` interface ([backend/db.go](backend/db.go)) instead of a concrete
`*pgxpool.Pool`. PostgreSQL works through it; build, `go vet`, and the full backend test suite pass.
The **jed backend is intentionally not wired yet** — blocked on the upstream jed durability+concurrency
fix (infelicity #1), which the maintainer is addressing first.

### Decisions (from design review)

1. **jed concurrency:** maintainer will fix the jed durable+concurrent-handle gap upstream before the
   jed backend is implemented here — so we deliberately do **not** work around it.
2. **Backend selection:** single `DATABASE_URL`, by scheme — `postgres://`/`postgresql://` → PostgreSQL,
   `jed:` → jed.
3. **SQL-query + MCP feature:** PostgreSQL-only for now; not mounted on the jed backend.
4. **Migrations:** deferred — likely belongs in jed/tern/a separate migration tool, not hand-rolled here.

---

## What we learned

### How the backend currently talks to the database

- Single driver: **pgx v5** (`pgxpool.Pool`). The pool is threaded explicitly into ~every handler
  (`handleX(pool *pgxpool.Pool)`).
- **~155 query call sites** across 11 files (`auth.go`, `logs.go`, `folders.go`, `sharing.go`,
  `passkeys.go`, `oauth_storage.go`, `oauth.go`, `middleware.go`, `mcp.go`, `sql_query.go`,
  `server.go`) using `QueryRow` / `Query` / `Exec` / `Begin`, all read via pgx `.Scan(&dest...)`.
- pgx-specific couplings beyond plain SQL:
  - `pgx.ErrNoRows` sentinel (no-row detection) — ~55 sites.
  - `pgconn.PgError` + SQLSTATE `23505` (unique-violation → friendly "already taken") — ~5 sites.
  - `pgx.Tx` / `pgx.Row` types passed around (`folders.go`).
  - **`sql_query.go`** (the user-facing "run SQL" + MCP feature) goes deep: `pgtype.Map`,
    `pgx.TxOptions{AccessMode: ReadOnly, IsoLevel: ReadCommitted}`, `QueryExecModeExec`,
    `QueryResultFormats(TextFormatCode)`, plus **`pgsqlarbiter-go`** for statement gating, a
    read-only restricted PG role, and per-user views.
- Migrations: **tern** (pgx-based), 13 files in `postgresql/migrations/`. They already target
  PG18-era builtins that jed also implements (`uuidv4()`, `uuidv7()`), plus `timestamptz`,
  `now()`, regex `CHECK`, partial + functional unique indexes, FKs with `ON DELETE CASCADE`, and
  `GRANT ... TO {{.app_user}}` (tern templating).

### jed's Go API (from source: `github.com/jackc/jed/impl/go`)

- **Not** pgx-compatible; **no** `database/sql` driver. Native API:
  - Open: `jed.Create(path, opts)` / `jed.Open(path)` → `*jed.Database` (file-backed, durable).
  - One-shot: `db.ExecuteSQL(sql, []jed.Value)` / `db.QuerySQL(sql, []jed.Value)`.
  - Tx: `db.View(fn)` / `db.Update(fn)` (bbolt-style) or `db.Begin(writable)`.
  - Params are **`[]jed.Value`** with `$N` placeholders (PG-compatible placeholder syntax 👍).
  - Results are **`[]jed.Value`** per row via `rows.Next()` / `rows.Row()`; values are
    kind-tagged (`ValInt`, `ValText`, `ValUuid`, `ValBytea`, `ValBool`, `ValJsonb`,
    `ValTimestamptz`, `ValDecimal`, arrays, composites…) with constructors (`IntValue`,
    `TextValue`, `UuidValue`, …) and accessors (`.Int`, `.Str`, `.Bool`, `.IsNull()`, `.Render()`).
  - Type names skew Rust-ish in examples (`i32`), but the SQL surface accepts PG type names.
- **Concurrency model — the catch.** jed has two disjoint handle families:
  - **`*Database` (file-backed, durable)** — fast/simple but **NOT goroutine-safe**; one handle
    cannot serve a reader and a writer concurrently (documented; race detector will flag it).
  - **`SharedDB` (`NewSharedDB`)** — proper bbolt-style concurrency (lock-free MVCC readers + a
    single-writer gate), but currently **in-memory only**. Source comment: *"file-backed sharing
    reuses the same publish point … and is wired when it lands."*
  - ⇒ **There is no durable + concurrent handle yet.** This is the central design fork for a
    concurrent HTTP server. See [Open questions](#open-design-questions) Q1 and
    [Infelicities](#infelicities-found-in-jed) #1.

---

## Architecture (implemented)

A single **`DB` seam** ([backend/db.go](backend/db.go)) — a Go interface that is a strict subset of
the pgx API we actually use (`Query`/`QueryRow`/`Exec`/`Begin`, plus `Row`/`Rows`/`CommandTag`/`Tx`).
Method names and signatures mirror pgx (including the leading `context.Context`) so the call sites are
unchanged apart from the parameter type.

- **`pgxDB`** (done) — thin pass-through over `*pgxpool.Pool`. pgx's concrete return types satisfy the
  subset interfaces, so each adapter method is a one-liner.
- **`jedBackend`** (TODO, blocked on jed fix) — will encode `args ...any` → `[]jed.Value`, decode
  `[]jed.Value` → `Scan(&dest...)` targets, and map jed errors onto the **shared error vocabulary**:
  `pgx.ErrNoRows` and `*pgconn.PgError` code `23505`. Adopting pgx's error sentinels as the common
  vocabulary means the handler-level error handling is identical for both backends.

`OpenDB(ctx, databaseURL)` picks the backend by URL scheme and returns the `DB` plus the concrete
`*pgxpool.Pool` (non-nil only for PostgreSQL). Core handlers take `DB`; the PostgreSQL-only features
(OAuth, MCP, the SQL-query feature) take the concrete pool and are mounted only when it is present.

> Naming note: in handlers the `DB`-typed parameter kept the name `pool` to minimize the conversion
> diff; in `server.go`/tests the `DB` value is `pool` and the concrete pool is `pgPool`.

---

## Open design questions

All resolved (see [Decisions](#decisions-from-design-review)). One still genuinely open and deferred:

- **Tests on jed.** Once the jed backend exists, run the Go suite against both backends (matrix),
  jed only (fast, dependency-free), or keep PG-only? Decide when the jed backend lands.

---

## TODO

- [x] Resolve open design questions.
- [x] Define the `DB` interface seam + `Row`/`Rows`/`CommandTag`/`Tx` abstractions.
- [x] Implement `pgxDB` (pass-through) and convert all core call sites off concrete `*pgxpool.Pool`.
- [x] Backend selection wiring (`OpenDB` by URL scheme; `server.go`).
- [x] Keep the SQL-query/MCP/OAuth features PostgreSQL-only (mounted only when a pool is present).
- [x] Keep build / `go vet` / backend tests green.
- [ ] **Blocked on jed:** upstream durable+concurrent handle (maintainer fixing — infelicity #1).
- [ ] Add jed dependency (`github.com/jackc/jed/impl/go`) once the handle lands.
- [ ] Implement `jedBackend` (param encode / row decode / error mapping onto pgx sentinels / concurrency).
- [ ] Verify the migration SQL runs on jed (DDL: `uuidv4()`/`uuidv7()`, `timestamptz`, regex `CHECK`,
      partial/functional unique indexes, FKs; and what `GRANT ... TO {{.app_user}}` should become on jed).
- [ ] Frontend: hide the SQL-query feature when running on jed (settings endpoint could advertise it).
- [ ] Test strategy for the jed backend + CI.
- [ ] Docs: update `CLAUDE.md` / `README` for the two-backend story.

---

## Infelicities found in jed

> The point of being first. Each entry: what was hit, where, and how we worked around it (only
> after asking).

1. **No durable + concurrent handle.** `Create`/`Open` give a durable but non-goroutine-safe
   `*Database`; `SharedDB` gives goroutine-safe MVCC but is in-memory only. A concurrent HTTP
   server that must persist data has no first-class option — you must either serialize a
   file-backed handle yourself or accept in-memory. (Status: raised with maintainer; awaiting
   direction.)
