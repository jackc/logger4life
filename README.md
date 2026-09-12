# Logger4Life

Logger4Life is a tool to quickly log recurring events. For example:

* Taking vitamins
* Counting pushups
* Changing diapers
* Standing up and stretching

## Features

### Custom Logs

Define custom log types for any kind of event you want to track. Each log gets its own quick-action button for one-tap logging.

### Custom Fields

Logs can have optional custom fields to capture additional data with each entry. Supported field types:

* **Text** - free-form text input
* **Number** - numeric values (including decimals)
* **Boolean** - yes/no values

Fields can be marked as required or optional. Each log supports up to 20 custom fields.

### Quick Logging

The home page provides a quick-log interface with cards for all your logs. Logs without required fields can be recorded in a single tap.

### Entry Management

* View all entries for a log, sorted by most recent
* Edit entries to update field values or correct the timestamp
* Delete entries you no longer need

### Log Sharing

Share your logs with other users so they can view and add entries:

* Generate a share link to invite others
* Revoke the share link at any time
* View who has access and remove individual users
* Shared users can view the log and create entries

### User Accounts

* Register with a username and password (email optional)
* Session-based authentication

## Tech Stack

* **Frontend** - SvelteKit 2 / Svelte 5 single-page app styled with Tailwind CSS 4
* **Backend** - Go API using Chi router
* **Database** - PostgreSQL with pgx and connection pooling, or an embedded
  [jed](https://github.com/jackc/jed) database file
* **Testing** - Go tests with testify (backend), Playwright (browser)
* **Build** - Vite (frontend), mise (tools, environment, and task orchestration), process-compose (development services)

## Development

### Prerequisites

Run `scripts/setup-host` on macOS (with Homebrew installed) or Ubuntu to
install PostgreSQL 18 and, on Ubuntu, Chromium's system dependencies. Ubuntu package
installation requires root or sudo. No server or cluster needs to be set up:
each checkout runs its own.

Install [mise](https://mise.jdx.dev) separately as the development user if it
is not already available.

### Getting Started

```sh
scripts/setup-host  # host packages (once per machine)
mise install        # tools
mise run dev:init   # ports and dependencies
mise run dev        # PostgreSQL + migrations + backend + Vite
```

`dev:init` installs the project's npm dependencies and Playwright Chromium as
the current user. On Ubuntu, `setup-host` installs Chromium's system libraries
and fonts; the browser download does not require root.

Ports are allocated per checkout rather than fixed, so several worktrees can
run at once. `mise run dev:urls` prints this one's:

```
Frontend:        http://localhost:23842
Backend:         http://localhost:23841
PostgreSQL:      127.0.0.1:23843
```

`mise run dev` is the only command that launches worktree services. Tests and
database commands use that running environment and fail with a startup hint
when it is absent.

See [docs/development-environment.md](docs/development-environment.md) for how
the environment is put together.

### Common Commands

| Command | Description |
|---------|-------------|
| `mise run dev` | Run the stack (PostgreSQL, backend, Vite) |
| `mise run dev:init` | Prepare a fresh checkout or worktree |
| `mise run dev:wait` | Wait for a detached stack to become ready |
| `mise run dev:down` | Stop a detached stack |
| `mise run dev:urls` | Print this checkout's ports and URLs |
| `mise run dev:browser` | Open the frontend in the default browser |
| `mise run db:psql` | psql against the development database |
| `mise run db:reset` | Drop and rebuild the databases |
| `mise run build` | Build everything (frontend assets + Go binary) |
| `mise run build:assets` | Build frontend assets |
| `mise run build:binary` | Build the native Go binary |
| `mise run test` | Run all tests |
| `mise run test:backend` | Run Go backend tests |
| `mise run test:browser` | Run Playwright browser tests |

Keep `mise run dev` running while using the test and database commands. CI and
agents can start it detached with `mise run dev -- -D`, wait with
`mise run dev:wait`, and stop it with `mise run dev:down` when finished.

## Database backends

PostgreSQL remains the default and the development stack continues to use it.
For a self-contained deployment, select the embedded jed backend and provide a
persistent data directory:

```sh
logger4life server \
  --database-backend jed \
  --jed-data-dir /var/lib/logger4life
```

The equivalent environment configuration is:

```sh
DATABASE_BACKEND=jed
JED_DATA_DIR=/var/lib/logger4life
```

Logger4Life creates and migrates
`/var/lib/logger4life/logger4life.jed` automatically. PostgreSQL uses the
existing `DATABASE_URL` setting; `DATABASE_BACKEND` defaults to `postgresql`.
Backend selection does not copy data between databases. See
[docs/database-backends.md](docs/database-backends.md) for configuration,
storage, backup details, and the development-only `both` comparison adapter.

## Deployment

Logger4Life can easily be deployed with [verna](https://github.com/jackc/verna).

The mise release tasks build artifacts suitable for deployment with verna,
and `deploy/caddy-handle-template.json` contains a preconfigured Caddy handle
template.

If these are used, then deployment is one-line command.

```
mise run build:linux-amd64 && verna app deploy build/linux_amd64.tar.gz
```

Set your verna config in `.mise.local.toml`. For example:

```toml
[env]

VERNA_SSH_HOST = "logger4life.example.com"
VERNA_APP = "logger4life"
```

## MCP (Model Context Protocol)

Logger4Life exposes five read-only tools to AI assistants over the
[Model Context Protocol](https://modelcontextprotocol.io). Set
`MCP_CANONICAL_URL` to enable the OAuth 2.1 authorization server
(`/oauth/...`) and the MCP endpoint (`/mcp`):

```
verna app env set MCP_CANONICAL_URL=https://logger4life.example.com
```

The value must be the public HTTPS origin, with no credentials, path, query,
or fragment. HTTP is allowed only on loopback hosts for local development
(`localhost`, IPv4 loopback, or `[::1]`). Startup rejects invalid values before
opening the database. A single trailing root slash is accepted and removed;
scheme/host case and default ports are normalized. The resulting origin is
used as both the OAuth issuer and the RFC 8707 audience binding for issued
access tokens, and is emitted exactly in OAuth issuer responses. International
hostnames must use their ASCII (punycode) form.

Consent POSTs require exactly one `Origin` header matching that public
origin; missing or foreign origins receive 403. Normal browser approval
forms supply this header automatically. Authorization pages deny framing.

The tools are `list_logs`, `get_sql_schema`, `run_sql`,
`list_saved_queries`, and `run_saved_query`. SQL queries are restricted to
the caller's data and capped at 1000 rows and 1 MiB of result values.

The endpoint supports protocol `2026-07-28` using stateless Streamable HTTP
with JSON responses, and retains compatibility with earlier clients using
`initialize`. Every request needs an OAuth bearer token. Browser requests
that include `Origin` must match the normalized public origin exactly.

See [resource limits](docs/resource-limits.md) for MCP throttling, SQL
concurrency, registration quotas, retention, and trusted proxy settings.

See the [MCP implementation review](docs/mcp-review.md) for the migration
details and remaining recommendations.

When adding the connector in a client (e.g. claude.ai → Settings →
Connectors → Add custom connector), provide the **full MCP endpoint
URL**, not just the origin:

```
https://logger4life.example.com/mcp
```

Some clients normalize the URL by stripping the path and treating the
origin as both the OAuth server and MCP endpoint; if you only provide
the origin, the OAuth handshake succeeds but the client's first MCP
request hits `/` (the SPA) instead of `/mcp` and fails opaquely.

## Production cookie hardening

When running behind HTTPS, set `SECURE_COOKIES=true` so the session
cookie gets the `Secure` attribute and won't be sent over plain HTTP:

```
verna app env set SECURE_COOKIES=true
```

The default is `false` so local dev over `http://localhost` keeps
working unchanged.
