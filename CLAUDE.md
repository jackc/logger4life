# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Logger4Life is a quick event logging tool (vitamins, pushups, diapers, etc.) with custom event types and optional attributes. It's a full-stack app with a Go backend API and Svelte SPA frontend, backed by PostgreSQL. Features include user authentication, custom log fields (text, number, boolean), log sharing via invite tokens, and full CRUD on logs and entries.

## Common Commands

### Development
Every checkout is a self-contained instance: its own PostgreSQL cluster, its
own randomly allocated port block, its own runtime state under `.dev/`. mise
owns tool versions, environment, and one-shot tasks; process-compose owns the
long-running services. See `docs/development-environment.md`.

- `mise run dev:init` — Prepare a checkout: ports, dependencies, PostgreSQL cluster, migrations
- `mise run dev` — Run PostgreSQL, the backend, and Vite under process-compose
- `mise run dev:urls` — Print this checkout's ports and URLs
- `mise run dev:down` — Stop the stack (or `process-compose down`)
- `process-compose process list` / `process-compose process logs backend` / `process-compose process restart backend` — Inspect and control services without the TUI; no flags needed, `PC_PORT_NUM` is in the environment
- Ports are never fixed. Read them from the environment (`BACKEND_URL`, `VITE_URL`, `PGPORT`) or `.dev/ports.env`; never assume 4000/5173/5432.

### Building
- `mise run build` (or `rake build`) — Build everything (frontend assets, Go binary, Linux binary)
- `rake build:assets` — Build frontend only (runs `npm run build` + zopfli compression)
- `rake build:binary` — Build Go binary only (`build/logger4life`)

### Testing
- `mise run test` (or `rake test`) — Run all tests; starts the cluster and prepares test databases
- `mise run test:backend` — Run Go backend tests; auto-prepares test databases
- `mise run test:browser` — Run Playwright browser tests (or `npm test`)
- `rake test:prepare` — Prepare test databases only
- `npm run test:report` — Show Playwright HTML report

### Database
- Migrations managed by **tern** (config: `postgresql/tern.conf`, migrations: `postgresql/migrations/`)
- Dev database: `logger4life_dev`; browser-test database: `logger4life_test`; Go database tests exclusively check out one of eight `logger4life_test_N` copies. All live in this checkout's own cluster under `.dev/<platform>/postgres/data` on the allocated `PGPORT`.
- `mise run db:psql`, `mise run db:migrate`, `mise run db:reset`, `mise run db:init`
- DB role: `logger4life`
- When creating tables, sequences, or other database objects in migrations, grant appropriate permissions to the `logger4life` role (e.g., `GRANT ALL ON TABLE ... TO logger4life`)

## Architecture

### Backend (Go) — `backend/`
- **CLI**: Cobra-based with subcommands (e.g., `server`). Entry point: `main.go` → `backend.Execute()` → `backend/root.go`
- **Layering**: `backend/domain` (pure rules) ← `backend/core` (action catalog and driven ports) ← `backend/pgstore` (PostgreSQL) and `backend/server` (HTTP/MCP adapters). See `docs/architecture.md`; `TestArchitecturalBoundaries` enforces the import rules.
- **HTTP**: Chi v5 router with structured request logging via `httplog/v3`; the port comes from `--port`/`PORT` (4000 only as a default). API routes under `/api/`.
- **Database**: pgx v5 with connection pooling (`pgxpool`). Connects to PostgreSQL.
- **Config**: Environment variables and CLI flags. Precedence: defaults < env vars < CLI flags. Env vars: `DATABASE_URL`, `BIND_ADDRESS`, `PORT`, `ALLOW_REGISTRATION`, `WEBAUTHN_RP_ID`, `WEBAUTHN_ORIGIN`, `LOG_LEVEL`, `LOG_FORMAT`, `MCP_CANONICAL_URL`, `SECURE_COOKIES`. CLI flags: `--database-url`, `--bind-address`, `--port`, `--allow-registration`, `--webauthn-rp-id`, `--webauthn-origin`, `--log-level`, `--log-format`, `--mcp-canonical-url`, `--secure-cookies`. Set `SECURE_COOKIES=true` in any HTTPS deployment so the session cookie gets the `Secure` attribute.
- **Logging**: Structured logging via `log/slog` and `httplog/v3`. `LOG_LEVEL` accepts `debug`, `info` (default), `warn`, `error`. `LOG_FORMAT` accepts `json` (default), `text`, or `journal` (logs directly to systemd journald via `slog-journal`).
- **Registration**: Disabled by default. Enable via `ALLOW_REGISTRATION=true` env var or `--allow-registration` CLI flag.
- UUIDs for primary keys (v7 preferred; v4 for users to hide creation time).

#### Backend Source Files
| File | Purpose |
|------|---------|
| `backend/root.go` | Cobra root command definition |
| `backend/server_cmd.go` | `server` subcommand: flags, config resolution, calls `server.Run` |
| `backend/architecture_test.go` | Enforces the layering import rules |
| `backend/server/server.go` | Composition root: pool, store, core, Chi router, route registration |
| `backend/server/config.go` | Config struct, default config, env var loader |
| `backend/server/auth.go` | Register, login, logout handlers; session/cookie management |
| `backend/server/logs.go` | Log and log entry HTTP handlers |
| `backend/server/folders.go` | Folder HTTP handlers |
| `backend/server/sharing.go` | Share link and membership HTTP handlers |
| `backend/server/passkeys.go` | WebAuthn request/response translation |
| `backend/server/sql_query.go` | User SQL and saved-query HTTP handlers |
| `backend/server/oauth.go` | OAuth 2.1 protocol translation (metadata, consent, redirects) |
| `backend/server/mcp.go` | MCP tool definitions and bearer-token middleware |
| `backend/server/middleware.go` | `loadSession` (cookie→user context) and `requireAuth` middleware |
| `backend/core/` | Action catalog, driven ports, sentinel errors |
| `backend/domain/` | Pure business types, validation, and protocol rules |
| `backend/pgstore/` | PostgreSQL implementations of every core port |

#### Authentication & Authorization
- Session-based auth via HTTP-only `session_token` cookie (hex-encoded, 32-byte random token)
- Sessions expire after 30 days
- Passwords hashed with bcrypt
- `loadSession` middleware loads user into request context on every request
- `requireAuth` middleware gates protected endpoints
- Log access is enforced in `backend/pgstore` by scoping every query to the caller's `user_log_placements` row, which exists for owners and shared members alike

#### API Routes

**Public (no auth):**
- `GET /api/settings` — Public app settings (`allow_registration`)
- `POST /api/register` — Create account (sets session cookie); returns 403 when registration is disabled
- `POST /api/login` — Authenticate (sets session cookie)

**Protected (auth required):**
- `POST /api/logout` — Clear session
- `GET /api/me` — Current user info
- `POST /api/logs` — Create log
- `GET /api/logs` — List logs (owned + shared)
- `GET /api/logs/{logID}` — Get log detail
- `DELETE /api/logs/{logID}` — Delete log (owner only)
- `POST /api/logs/{logID}/entries` — Create entry
- `GET /api/logs/{logID}/entries` — List entries (ordered by occurred_at DESC)
- `PUT /api/logs/{logID}/entries/{entryID}` — Update entry
- `DELETE /api/logs/{logID}/entries/{entryID}` — Delete entry
- `POST /api/logs/{logID}/share-token` — Generate share token (owner only)
- `DELETE /api/logs/{logID}/share-token` — Revoke share token (owner only)
- `GET /api/logs/{logID}/shares` — List shared users (owner only)
- `DELETE /api/logs/{logID}/shares/{shareID}` — Remove user from shares (owner only)
- `GET /api/join/{token}` — Preview shared log info
- `POST /api/join/{token}` — Join a shared log

#### Custom Fields
- Logs support up to 20 field definitions, each with name, type (`text`, `number`, `boolean`), and required flag
- Field definitions stored as JSONB in `logs.fields`; entry values stored as JSONB in `log_entries.fields`
- Validation in `domain.ValidateFieldDefinitions()` and `domain.ValidateFieldValues()` (in `backend/domain/logs.go`)

#### Sharing Model
- Owner generates a 32-byte share token stored on the log
- Other users join via the token, creating a `log_shares` row
- Shared members can CRUD entries but cannot manage shares or delete the log

### Frontend (SvelteKit) — `src/`
- **SvelteKit 2 + Svelte 5** with static adapter (SPA mode: no SSR, no prerendering)
- **Styling**: Tailwind CSS 4 via `@tailwindcss/vite` plugin
- **API client**: `src/lib/api.js` — thin wrappers (`apiGet`, `apiPost`, `apiPut`, `apiDelete`) around fetch
- **Auth state**: `src/lib/auth.svelte.js` — singleton reactive module using `$state` with exported `getAuth()`, `checkAuth()`, `login()`, `register()`, `logout()`
- **App settings**: `src/lib/settings.svelte.js` — singleton reactive module for server settings (`allowRegistration`); loaded in layout
- Vite dev server proxies `/api` requests to `$BACKEND_URL` and binds `$VITE_PORT` with `strictPort`

#### Routes
| Route | File | Purpose |
|-------|------|---------|
| `/` | `+page.svelte` | Landing page (logged out) or quick-log dashboard (logged in) |
| `/login` | `login/+page.svelte` | Login form |
| `/register` | `register/+page.svelte` | Registration form (shows disabled message when registration is off) |
| `/logs` | `logs/+page.svelte` | Log management: create logs with custom fields, list/delete logs |
| `/logs/{id}` | `logs/[id]/+page.svelte` | Log detail: create/edit/delete entries, share panel (owner) |
| `/join/{token}` | `join/[token]/+page.svelte` | Accept shared log invitations |
| `/me` | `me/+page.svelte` | Account info page |

#### Svelte 5 Patterns
- `$state` for reactive variables, `$derived` for computed values, `$effect` for side effects
- `{@render children()}` for slot rendering in layouts
- `bind:value` / `bind:checked` for form inputs
- Auth state checked via `$effect` on each page, redirecting as needed

### Database Schema

7 migrations in `postgresql/migrations/`:

| Table | Key Columns | Notes |
|-------|-------------|-------|
| `users` | id (UUIDv4), username, email, password_hash | Case-insensitive unique username/email |
| `sessions` | id (UUIDv7), user_id, token (bytea), expires_at | 30-day expiry, ON DELETE CASCADE |
| `logs` | id (UUIDv7), user_id, name, fields (jsonb), share_token (bytea) | Unique (user_id, name) |
| `log_entries` | id (UUIDv7), log_id, fields (jsonb), occurred_at, updated_at | Indexed by (log_id, occurred_at DESC) |
| `log_shares` | id (UUIDv7), log_id, user_id | Unique (log_id, user_id) |

### Testing
- **Backend**: Go tests with **testify** in `backend/server/` (adapter and end-to-end coverage) and `backend/core/` (action unit tests over fake ports). `backend/pgstore/` holds store conformance tests. `rake test:prepare` migrates `logger4life_test`, installs `pgundolog`, and clones eight databases. PostgreSQL tests run in parallel and exclusively check out a clone through `github.com/jackc/testdb`; `pgundolog` resets the clone on reuse. Run through `rake test:backend` so the database pool is prepared first.
- **Browser**: **Playwright** (Chromium only) in `tests/` — `auth.spec.js`, `home.spec.js`, `logs.spec.js`. Playwright auto-starts both Vite dev server and Go backend.

### Build Artifacts
- Frontend assets → `build/assets/` (with `.gz` compressed copies via zopfli)
- Go binary → `build/logger4life` (native) and `build/logger4life-linux` (cross-compiled)

### Tooling
- **mise** (`.mise.toml`) manages tool versions, the worktree environment, and the task interface
- **process-compose** (`process-compose.yaml`) supervises the development services
- **scripts/** holds the shell out of the task definitions: `devports` (port allocation), `dev-env.sh` (environment assembly), `dev-init`, `dev-up`, `db-init`, `db-reset`, `db-ensure-running`, `pgbin`
- **Bundler** (`Gemfile`) for Ruby/Rake dependencies
- Dev container setup in `.devcontainer/` (Ubuntu 24.04 + PostgreSQL 18 binaries + mise); it is a thin Linux shell that runs the same `mise run dev:init` / `mise run dev` as native macOS
- **fd** and **rg** (ripgrep) are available in the dev container — use them instead of `find` and `grep`
