# Database backends

Logger4Life supports PostgreSQL and [jed](https://github.com/jackc/jed) behind
the same application persistence ports. Backend selection happens once at
server startup; the HTTP, MCP, and domain layers are identical for both.

## Selecting a backend

Configuration precedence is defaults, then environment variables, then CLI
flags.

| Backend | Environment | CLI |
|---|---|---|
| PostgreSQL (default) | `DATABASE_BACKEND=postgresql` and `DATABASE_URL=...` | `--database-backend postgresql --database-url ...` |
| jed | `DATABASE_BACKEND=jed` and `JED_DATA_DIR=...` | `--database-backend jed --jed-data-dir ...` |
| Both (validation only) | `DATABASE_BACKEND=both`, `DATABASE_URL=...`, and `JED_DATA_DIR=...` | `--database-backend both --database-url ... --jed-data-dir ...` |

An empty backend setting is treated as PostgreSQL for compatibility with
older configuration. Any other value fails startup with a configuration
error.

## PostgreSQL

PostgreSQL behavior is unchanged. Schema migrations remain in
`postgresql/migrations/` and are applied with the existing tern/mise tasks.
The development stack uses PostgreSQL by default.

## jed

The jed backend is embedded in the Logger4Life process and does not require a
database server. At startup it creates `JED_DATA_DIR` with owner-only
permissions when needed, opens `JED_DATA_DIR/logger4life.jed`, and applies the
embedded migrations from `db/migrations/jed/`.

Use a persistent absolute directory in production, for example:

```sh
DATABASE_BACKEND=jed \
JED_DATA_DIR=/var/lib/logger4life \
logger4life server
```

Back up the `logger4life.jed` file while the Logger4Life process is stopped so
the copy represents one complete database image.

## Both (validation harness)

The `both` backend follows fam's fail-stop comparison pattern. It runs every
persistence operation against PostgreSQL first and jed second, returns the
PostgreSQL result, and panics if their observable results or domain errors
differ. It is a development and test mode, not a high-availability or
production mirroring mode.

Both databases must start with equivalent data. A crash between their separate
commits can leave them out of sync; that is acceptable for this validation
harness and will be reported as a later divergence. The backend health check
also requires both databases to respond.

## Moving data

Selecting a backend does not migrate or mirror existing data. PostgreSQL and
jed keep independent databases, and this project does not currently include a
cross-backend import/export command.

PostgreSQL, jed, and the dual adapter run the shared
`backend/core/storetest` conformance suite. `rake test:backend` also runs the
HTTP server suite against each of `postgresql`, `jed`, and `both`, so a change
that behaves differently between engines fails the normal backend test task.
The conformance suite covers authentication, logs and entries, folders and
placements, sharing, passkeys, saved SQL, OAuth, user-authored read-only SQL,
and the transaction contract expected by core actions.
