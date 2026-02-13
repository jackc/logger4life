# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Logger4Life is a quick event logging tool (vitamins, pushups, diapers, etc.) with custom event types and optional attributes. It's a full-stack app with a Go backend API and Svelte SPA frontend, backed by PostgreSQL. Features include user authentication, custom log fields (text, number, boolean), log sharing via invite tokens, and full CRUD on logs and entries.

## Common Commands

### Development
- `npm run dev` — Start Vite dev server (port 5173)
- `rake run` — Build and run the Go backend (port 4000); requires Vite dev server running separately
- `rake rerun` — Watch for Go/backend changes and auto-rebuild/restart

### Building
- `rake build` — Build everything (frontend assets, Go binary, Linux binary)
- `rake build:assets` — Build frontend only (runs `npm run build` + zopfli compression)
- `rake build:binary` — Build Go binary only (`build/logger4life`)

### Testing
- `rake test` — Run Go backend tests (`go test ./...`); auto-prepares test databases
- `rake test:browser` — Run Playwright browser tests (or `npm test`)
- `rake test:prepare` — Prepare test databases only
- `npm run test:report` — Show Playwright HTML report

### Database
- Migrations managed by **tern** (config: `postgresql/tern.conf`, migrations: `postgresql/migrations/`)
- Dev database: `logger4life_dev`, test database: `logger4life_test` (both on local PostgreSQL)
- DB role: `logger4life_app`
- When creating tables, sequences, or other database objects in migrations, grant appropriate permissions to the `logger4life_app` role (e.g., `GRANT ALL ON TABLE ... TO logger4life_app`)

## Architecture

### Backend (Go) — `backend/`
- **CLI**: Cobra-based with subcommands (e.g., `server`). Entry point: `main.go` → `backend.Execute()` → `backend/root.go`
- **HTTP**: Chi v5 router on port 4000 with `middleware.Logger`. API routes under `/api/`.
- **Database**: pgx v5 with connection pooling (`pgxpool`). Connects to PostgreSQL.
- **Config**: `logger4life.conf` with `database_url`, `listen_address`, and `allow_registration` settings. Config file is parsed when `--config` flag is provided. CLI flags override config file values.
- **Registration**: Disabled by default. Enable via `allow_registration=true` in config file or `--allow-registration` CLI flag (flag overrides config).
- UUIDs for primary keys (v7 preferred; v4 for users to hide creation time).

#### Backend Source Files
| File | Purpose |
|------|---------|
| `server.go` | HTTP server setup, Chi router, route registration, settings endpoint |
| `config.go` | Config struct, default config, config file parser |
| `auth.go` | Register, login, logout handlers; session/cookie management |
| `logs.go` | Log and log entry CRUD; field definition/value validation |
| `sharing.go` | Share token generation, join flow, share management, access control |
| `middleware.go` | `loadSession` (cookie→user context) and `requireAuth` middleware |
| `root.go` | Cobra root command definition |

#### Authentication & Authorization
- Session-based auth via HTTP-only `session_token` cookie (hex-encoded, 32-byte random token)
- Sessions expire after 30 days
- Passwords hashed with bcrypt
- `loadSession` middleware loads user into request context on every request
- `requireAuth` middleware gates protected endpoints
- `checkLogAccess()` in `sharing.go` verifies the requesting user is either the log owner or a shared member

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
- Validation in `validateFieldDefinitions()` and `validateFieldValues()` (in `logs.go`)

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
- Vite dev server proxies `/api` requests to `http://localhost:4000`

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
- **Backend**: Go tests with **testify** in `backend/auth_test.go` and `backend/logs_test.go`. `setupTestRouter()` creates a test server against `logger4life_test` DB and cleans up between tests.
- **Browser**: **Playwright** (Chromium only) in `tests/` — `auth.spec.js`, `home.spec.js`, `logs.spec.js`. Playwright auto-starts both Vite dev server and Go backend.

### Build Artifacts
- Frontend assets → `build/assets/` (with `.gz` compressed copies via zopfli)
- Go binary → `build/logger4life` (native) and `build/logger4life-linux` (cross-compiled)

### Tooling
- **mise** (`.mise.toml`) manages Go, Node.js, and Ruby versions
- **Bundler** (`Gemfile`) for Ruby/Rake dependencies
- Dev container setup in `.devcontainer/` (Ubuntu 24.04, PostgreSQL 18, auto-installs tern + watchexec)
- **fd** and **rg** (ripgrep) are available in the dev container — use them instead of `find` and `grep`
