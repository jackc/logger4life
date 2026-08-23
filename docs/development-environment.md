# Development environment

Every checkout of this repository is a self-contained development instance:
its own PostgreSQL cluster, its own ports, its own runtime state. Two
worktrees can run at the same time without knowing about each other, and the
same commands work natively on macOS and inside the dev container.

```
git worktree      = instance
mise              = tool versions, environment, one-shot tasks
process-compose    = the long-running services
.dev/             = instance-local state (git-ignored)
```

## Getting started

```sh
mise install        # tools: Go, Node, Ruby, tern, process-compose, ...
mise run dev:init   # ports, dependencies, PostgreSQL cluster, migrations
mise run dev        # PostgreSQL + backend + Vite
```

`mise run dev:init` is idempotent; run it again after pulling changes that add
a dependency or a migration.

PostgreSQL itself is the one prerequisite mise does not install:

```sh
brew install postgresql@18      # macOS
apt-get install postgresql-18   # Debian/Ubuntu; the dev container does this
```

Only the binaries are needed. No machine-wide cluster or service is used —
each worktree runs `initdb` into its own `.dev/` directory.

## Ports

Nothing in this project has a fixed port. When a worktree is initialized it
draws a random block of 20 ports and persists it in `.dev/ports.env`:

```
PORT_BASE=23840
BACKEND_PORT=23841        base + 1
VITE_PORT=23842           base + 2
PGPORT=23843              base + 3
PC_PORT_NUM=23844         base + 4   process-compose control API
DEBUG_PORT=23845          base + 5
TEST_BACKEND_PORT=23846   base + 6
TEST_VITE_PORT=23847      base + 7
PLAYWRIGHT_REPORT_PORT=23848  base + 8
```

`mise` loads that file, so every task, test run, and shell in the worktree
agrees on the ports, and `process-compose` finds its own instance without
flags. To see them:

```sh
mise run dev:urls
```

Two dormant worktrees may draw the same block; that is harmless. Before
starting anything, `mise run dev` re-checks the block and draws a new one if
another process now holds a port — the operating system is the registry of
the allocations that currently matter. `mise run dev:ports:reset` forces a new
block.

Services bind to `127.0.0.1`. The URLs are spelled `localhost` because
WebAuthn will not accept an IP address as a relying party ID and this app has
passkeys.

## The stack

`mise run dev` runs [process-compose](https://github.com/F1bonacc1/process-compose)
over [`process-compose.yaml`](../process-compose.yaml):

```
postgres  ->  migrate  ->  backend  ->  vite
```

Each arrow is a real dependency: `migrate` waits until `pg_isready` succeeds,
`backend` waits until the migrations exit successfully, `vite` waits until the
backend answers `/health`. The backend runs under `watchexec`, so editing Go
code restarts it.

The TUI is the default view. Everything is also scriptable:

```sh
process-compose process list             # status
process-compose process logs backend     # one service's output
process-compose process restart backend  # restart one service
process-compose down                     # stop the stack
```

No `--port` is needed: `PC_PORT_NUM` is part of the worktree environment, so
the CLI finds this worktree's instance.

`process-compose process logs` is the current view of a service's output.
Copies are also written to `.dev/logs/`, but those are flushed as output
accumulates, so a quiet process may lag behind.

## The database

Each worktree has a complete PostgreSQL cluster under
`.dev/<platform>/postgres/data` (the platform is in the path because a data
directory built by the dev container cannot be read by a native macOS
PostgreSQL, and one worktree may be opened both ways).

It holds `logger4life_dev` and `logger4life_test`, listens only on loopback,
and uses trust authentication — safe for a cluster whose port nothing else
knows, and one less secret in the environment.

| Command | Description |
|---------|-------------|
| `mise run db:psql` | psql against the development database |
| `mise run db:migrate` | run pending migrations |
| `mise run db:reset` | drop both databases and rebuild from migrations |
| `mise run db:init` | create the cluster if absent, then migrate |

The database tasks and the test suites start the cluster if it is not already
running, so `rake test` works whether or not `mise run dev` is up. To throw the
cluster away entirely, delete `.dev/<platform>/postgres`.

## Tests

```sh
mise run test           # everything
mise run test:backend   # Go
mise run test:browser   # Playwright
```

The browser suite starts its own backend and Vite on the worktree's reserved
test ports, against the worktree's own `logger4life_test`.

## The dev container

The container is a thin Linux shell around this same interface. It installs
mise and the PostgreSQL binaries, then runs the same `mise run dev:init`. It
does not define services of its own — no `db` service, no fixed forwarded
ports — so behavior inside it matches native macOS.

Use it for Linux parity: reproducing production behavior, checking libc
differences, or stronger isolation. Use native macOS for the fast inner loop.

## What lives where

```
repository (shared through git)     worktree-local (.dev/, git-ignored)
  mise.toml, tasks                    ports.env
  process-compose.yaml                PostgreSQL cluster
  scripts/                            process-compose logs
  source                              build output
```
