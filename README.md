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

### Entry Notes

Every entry can include an optional Markdown note of up to 20,000 characters.
Use **Add note** on a quick-log card or log page, with Write and Preview modes.
Notes support headings, lists, emphasis, links, blockquotes, and code; raw HTML
and embedded images are disabled. Long notes collapse in entry history and can
be expanded or edited later. Shared notes follow the entry's permissions.

Notes are stored as Markdown text and available as `log_entries.note` in SQL.
API entry creation accepts `note`; updates preserve it when omitted and clear it
when sent as an empty string. Apply PostgreSQL migration **017** before running
this version; jed migration **005** applies automatically.

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

* **Frontend** - SvelteKit 3 / Svelte 5 single-page app styled with Tailwind CSS 4
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

Logger4Life exposes 28 tools to AI assistants over the
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

Clients can use a publicly hosted HTTPS **Client ID Metadata Document** as
their `client_id`, without calling `/oauth/register`. Discovery advertises
`client_id_metadata_document_supported: true`; DCR remains available for
existing clients. For example, serve this at
`https://client.example.com/oauth/client.json` with status 200 and
`Content-Type: application/json`:

```json
{
  "client_id": "https://client.example.com/oauth/client.json",
  "client_name": "Example MCP Client",
  "redirect_uris": ["http://127.0.0.1:3000/callback"],
  "token_endpoint_auth_method": "none",
  "grant_types": ["authorization_code", "refresh_token"],
  "response_types": ["code"]
}
```

The document URL must include a path, contain no credentials, fragment,
or dot path segments, and match `client_id` exactly. Redirects also match
exactly against the document. Metadata must explicitly declare the public
client authentication method `none`; confidential authentication methods,
secrets, and embedded keys are unsupported. Omitting `grant_types` defaults
to `authorization_code` and produces no refresh token. Add `refresh_token`
to receive refresh tokens. If supplied, `scope` must be `mcp`.

Documents are fetched only over public HTTPS, including in local development;
HTTP redirects and private/special-use destinations are rejected. Loopback
**callbacks** remain supported. Names and hostnames are shown on consent,
but remote logos and other linked resources are never loaded. Cache headers
control reuse within a one-hour cap. See [CIMD limits and lifecycle](docs/resource-limits.md#client-id-metadata-documents).

Apply PostgreSQL migration **016** before running this version; jed applies
migration **004** automatically. Existing DCR codes and tokens keep their
behavior. Metadata changes affect new authorizations; issued codes and token
families keep their original URL identity, callback, scope, and grant policy.
Code exchange and refresh re-check the document: if its host answers 404 or
410, or the URL now names another client, the client is refused until the
document returns. Fetch failures and outages leave existing grants working.

The read tools are `list_logs`, `get_log`, `get_log_entry`, `list_folders`,
`get_folder`, `list_shared_users`, `get_share_info`, `get_sql_schema`, `run_sql`,
`list_saved_queries`, and `run_saved_query`. Log detail includes the caller's
folder placement, position, and home pin state. SQL remains read-only,
restricted to the caller's data, and capped at 1000 rows and 1 MiB of values.

The write tools are:

* Entries: `create_log_entry`, `update_log_entry`, `delete_log_entry`.
* Logs: `create_log`, `update_log`, `delete_log`.
* Folders: `create_folder`, `rename_folder`, `move_folder`, `delete_folder`.
* Organization: `update_log_placement`, `pin_log`, `update_log_home_position`.
* Sharing: `create_share_token`, `delete_share_token`, `join_shared_log`,
  `remove_shared_user`.

Saved-query writes are not exposed. All tools use the existing `mcp` OAuth
scope and existing ownership/membership rules; existing MCP tokens can call
write tools. Shared members can create, update, and delete entries, but only
owners can change log definitions, delete logs, or manage invitations and
memberships. Folder and home organization are personal to the caller.

`create_log_entry` accepts an optional RFC3339 `occurred_at` with a timezone,
defaulting to now. This timestamp is also supported by the HTTP create-entry
endpoint. MCP `update_log_entry` patches only supplied values: omitted field
names, timestamps, and notes are preserved; a field value of `null` removes
an optional field; `note: ""` clears the note. Number field values must be
strings, such as `"25"`. The HTTP PUT endpoint retains its replacement-field
and required-timestamp contract. `update_log` also requires the complete name
and field definition list; it does not migrate historical entries.

Every MCP write accepts an optional `idempotency_key` of at most 128 bytes.
Use a fresh key for each intended operation and reuse it on retries. Keys
are scoped to the authenticated user and tool name. Identical decoded
arguments return the original successful response, including its original
timestamps, without repeating the mutation; changed arguments with the same
key fail. Concurrent retries serialize in the database. Failed operations
roll back both the mutation and receipt, so their keys can be retried.
Receipts persist across restarts and do not expire; they retain the original
response even if the affected record is later edited or deleted. Without a
key, repeating creation creates another record where uniqueness permits.

Writes return the resulting record, including placement after organization
changes. Entry, log, folder, and membership deletion return a receipt with
`id`, `deleted`, and, where applicable, `log_id`. Invitation revocation returns
the resulting log; it does not remove existing members. The `id` in
`list_shared_users` is a membership ID for `remove_shared_user.share_id`.
New direct reads and writes have a five-second deadline and a 256 KiB result
budget; oversized write results roll back the operation.

Apply PostgreSQL migration **019** before using MCP writes. Embedded jed
migration **007** applies automatically. Both add persistent retry receipts;
the receipts are private implementation data, unavailable through `run_sql`.

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
