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
* **Backend** - Go API using Chi router, served on port 4000
* **Database** - PostgreSQL with pgx and connection pooling
* **Testing** - Go tests with testify (backend), Playwright (browser)
* **Build** - Vite (frontend), Rake tasks (build orchestration), mise (tool versions)

## Development

### Prerequisites

* Go 1.25+
* Node.js (managed via mise)
* PostgreSQL
* Ruby + Bundler (for Rake tasks)
* [tern](https://github.com/jackc/tern) (database migrations)

### Getting Started

1. Set up the PostgreSQL databases (`logger4life_dev` and `logger4life_test`) and the `logger4life` role.
2. Run database migrations:
   ```sh
   tern migrate --config postgresql/tern.conf --migrations postgresql/migrations
   ```
3. Start the frontend dev server:
   ```sh
   npm run dev
   ```
4. In a separate terminal, build and run the backend:
   ```sh
   rake run
   ```

The app will be available at `http://localhost:5173` with API requests proxied to port 4000.

### Common Commands

| Command | Description |
|---------|-------------|
| `npm run dev` | Start Vite dev server (port 5173) |
| `rake run` | Build and run the Go backend (port 4000) |
| `rake rerun` | Watch for backend changes and auto-restart |
| `rake build` | Build everything (frontend assets + Go binary) |
| `rake test` | Run all tests |
| `rake test:backend` | Run Go backend tests |
| `rake test:browser` | Run Playwright browser tests |

## Deployment

Logger4Life can easily be deployed with [verna](https://github.com/jackc/verna).

There are rake tasks that build artifacts suitable for deployment with verna and `deploy/caddy-handle-template.json`
contains a preconfigured Caddy handle template.

If these are used, then deployment is one-line command.

```
rake build/linux_amd64.tar.gz && verna app deploy build/linux_amd64.tar.gz
```

Set your verna config in `.mise.local.toml`. For example:

```toml
[env]

VERNA_SSH_HOST = "logger4life.example.com"
VERNA_APP = "logger4life"
```

## MCP (Model Context Protocol)

Logger4Life can expose a `list_logs` tool to AI assistants over the
[Model Context Protocol](https://modelcontextprotocol.io). Set
`MCP_CANONICAL_URL` to enable the OAuth 2.1 authorization server
(`/oauth/...`) and the MCP endpoint (`/mcp`):

```
verna app env set MCP_CANONICAL_URL=https://logger4life.example.com
```

The value must be the public origin without a trailing slash. It is used
as both the OAuth issuer and the RFC 8707 audience binding for issued
access tokens.

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
