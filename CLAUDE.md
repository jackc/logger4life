# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Logger4Life is a quick event logging tool (vitamins, pushups, diapers, etc.) with custom event types and optional attributes. It's a full-stack app with a Go backend API and Svelte SPA frontend, backed by PostgreSQL.

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

## Architecture

### Backend (Go) — `backend/`
- **CLI**: Cobra-based with subcommands (e.g., `server`). Entry point: `main.go` → `backend.Execute()` → `backend/root.go`
- **HTTP**: Chi router on port 4000 with `middleware.Logger`. API routes under `/api/`.
- **Database**: pgx with connection pooling (`pgxpool`). Connects to PostgreSQL.
- UUIDs for primary keys (v7 preferred; v4 when creation time should be hidden).

### Frontend (SvelteKit) — `src/`
- **SvelteKit 2 + Svelte 5** with static adapter (SPA mode: no SSR, no prerendering)
- **Styling**: Tailwind CSS 4 via `@tailwindcss/vite` plugin
- Routes in `src/routes/`; uses Svelte 5 runes (`$state`, `$effect`)

### Testing
- Backend: Go tests with **testify**
- Browser: **Playwright** (Chromium, tests in `tests/`, base URL `http://localhost:5173`)
- Playwright auto-starts Vite dev server when running tests

### Build Artifacts
- Frontend assets → `build/assets/` (with `.gz` compressed copies via zopfli)
- Go binary → `build/logger4life` (native) and `build/logger4life-linux` (cross-compiled)

### Tooling
- **mise** (`.mise.toml`) manages Node.js and Ruby versions
- **Bundler** (`Gemfile`) for Ruby/Rake dependencies
- Dev container setup in `.devcontainer/`
