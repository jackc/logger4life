# Development environment

Every checkout of this repository is a self-contained development instance:
its own PostgreSQL cluster, its own ports, its own runtime state. Two
worktrees can run at the same time without knowing about each other, and the
same commands work natively on macOS and inside the dev container.

```
git worktree      = instance
mise              = tool versions, environment, finite tasks
process-compose    = service profiles and service lifecycles
.dev/ + .port-tamer.env = instance-local state (git-ignored)
```

## Getting started

```sh
mise install        # tools: Go, Node, Port Tamer, tern, process-compose, ...
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

Nothing in this project has a fixed port. When a task first needs ports,
[Port Tamer](https://github.com/jackc/port-tamer) chooses an available,
consecutive group and persists it in `.port-tamer.env`:

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

The names and their order live in [`port-tamer.toml`](../port-tamer.toml).
Port Tamer owns only those assignments; mise and `scripts/dev-env.sh` derive
this project's URLs and database connection strings from them.

`mise` loads the saved assignments, so every task, test run, and shell in the
worktree agrees on the ports, and `process-compose` finds its own instance
without flags. To see the project URLs:

```sh
mise run dev:urls
```

`port-tamer allocate` checks the complete group when it creates an allocation.
Later calls preserve that group even when its ports are listening, because the
listeners may be this worktree's own services. `port-tamer status` reports the
saved assignments and their current availability. After stopping the stack,
`mise run dev:ports:reset` deliberately chooses a different group.

Services bind to `127.0.0.1`. The URLs are spelled `localhost` because
WebAuthn will not accept an IP address as a relying party ID and this app has
passkeys.

## The stack

`mise run dev` acquires the `dev` profile from
[process-compose](https://github.com/F1bonacc1/process-compose), whose service
catalog lives in [`process-compose.yaml`](../process-compose.yaml):

```
postgres  ->  database-ready  ->  backend  ->  vite
```

Each arrow is a real dependency. `database-ready` creates the roles and
databases and applies migrations after this supervisor's PostgreSQL is ready;
the backend waits for that to complete; Vite waits until the backend answers
`/health`. Process Compose watches the backend's Go source and module files,
so editing them rebuilds and restarts the backend.

There is one process-compose supervisor per running worktree. `scripts/services`
starts it or connects to it and exposes three profiles:

| Profile | Services made ready for |
|---------|-------------------------|
| `db` | database commands such as `psql` and migrations |
| `test` | backend and browser test tasks |
| `dev` | the interactive backend and frontend stack |

Mise still owns all finite tasks. `scripts/with-services PROFILE -- COMMAND`
only acquires the requested service profile before running a mise task. If a
supervisor was already running, the task borrows it. Otherwise the wrapper
starts a temporary detached supervisor and shuts it down when the task exits.
No mise task launches a profile service directly; task-local fixtures such as
Playwright's temporary backend and Vite processes remain owned by that task.

Each borrower holds a PID-backed lease. Concurrent tasks may acquire different
profiles and share services; the supervisor is stopped only after the final
lease is released. Leases left by a killed task are pruned by the next service
operation.

The supervisor records the generation of the service catalog and its lifecycle
scripts when it starts. If those files change while it is running, the next
service-backed task asks for `mise run dev:down` instead of hot-reloading and
silently restarting shared dependencies underneath another task.

The TUI is the default view. Everything is also scriptable:

```sh
process-compose process list             # status
process-compose process logs backend     # one service's output
process-compose process restart backend  # restart one service
mise run dev:down                        # stop the supervisor and its services
```

No `--port` is needed: `PC_PORT_NUM` is part of the worktree environment, so
the CLI finds this worktree's instance.

`process-compose process logs` is the current view of a service's output.
Copies are also written to `.dev/logs/`, but those are flushed as output
accumulates, so a quiet process may lag behind. The supervisor log is
`.dev/process-compose.log`; client commands use
`.dev/process-compose-client.log` so they cannot truncate the supervisor log.

## The database

Each worktree has a complete PostgreSQL cluster under
`.dev/<platform>/postgres/data` (the platform is in the path because a data
directory built by the dev container cannot be read by a native macOS
PostgreSQL, and one worktree may be opened both ways).

It holds `logger4life_dev`, the browser-test database `logger4life_test`, and
eight `logger4life_test_N` copies used by parallel Go tests. It listens only
on loopback and uses trust authentication — safe for a cluster whose port
nothing else knows, and one less secret in the environment.

| Command | Description |
|---------|-------------|
| `mise run db:psql` | psql against the development database |
| `mise run db:migrate` | run pending migrations |
| `mise run db:reset` | drop both databases and rebuild from migrations |
| `mise run db:init` | create the cluster if absent, then migrate |

Database tasks acquire the `db` service profile, and test tasks acquire the
`test` profile. Both reuse the development supervisor when it is running or
use a temporary supervisor otherwise, so `mise run test` works with or without
`mise run dev`. PostgreSQL is always a child of process-compose. To throw the
cluster away entirely, run `mise run dev:down` and delete
`.dev/<platform>/postgres`.

## Tests

```sh
mise run test           # everything
mise run test:backend   # Go
mise run test:browser   # Playwright
```

The browser suite starts its own backend and Vite on the worktree's reserved
test ports, against the worktree's own `logger4life_test`.

The test commands themselves remain mise tasks; process-compose knows only the
services in the `test` profile and their readiness relationships. Adding Redis,
NATS, or another test dependency means adding it to that profile, without
changing the task wrapper.

Go tests that reach PostgreSQL run concurrently. `mise run test:prepare` migrates
the primary test database once, installs `pgundolog`, and clones it eight
times. Each test exclusively checks out a clone through
`github.com/jackc/testdb`; checkout is coordinated in PostgreSQL across test
package processes, and `pgundolog` restores the clone before it is reused.
`mise run test:backend` then reruns the server suite with the jed and `both`
adapters; `both` executes each persistence call against PostgreSQL and jed and
fails immediately if their observable behavior differs.

## The dev container

The container is a thin Linux shell around this same interface. It installs
mise and the PostgreSQL binaries, then runs the same `mise run dev:init`. It
does not define services of its own — no `db` service, no fixed forwarded
ports — so behavior inside it matches native macOS.

Use it for Linux parity: reproducing production behavior, checking libc
differences, or stronger isolation. Use native macOS for the fast inner loop.

## What lives where

```
repository (shared through git)     worktree-local (git-ignored)
  mise.toml, tasks                    .port-tamer.env
  port-tamer.toml                     .dev/ PostgreSQL cluster
  process-compose.yaml, profiles      .dev/ process-compose logs
  mise.toml, finite tasks             build output
  scripts/ and source
```
