# shellcheck shell=bash
#
# Sourced by the other scripts in this directory, never run directly. Assumes
# the caller has already changed to the worktree root and set -e.
#
# This is the one place where a worktree's environment is assembled: the
# ports persisted by port-tamer, the project-specific values derived from
# them, the paths of this worktree's PostgreSQL cluster, and the PostgreSQL
# binaries to run it with.

set -a

port-tamer allocate
. .port-tamer.env

# port-tamer deliberately owns only named port assignments. URLs, database
# names, and other application settings belong to this project.
BIND_ADDRESS="127.0.0.1"
BACKEND_URL="http://localhost:$BACKEND_PORT"
VITE_URL="http://localhost:$VITE_PORT"
TEST_BACKEND_URL="http://localhost:$TEST_BACKEND_PORT"
TEST_VITE_URL="http://localhost:$TEST_VITE_PORT"
PGHOST="127.0.0.1"
DATABASE_URL="postgres://postgres@$PGHOST:$PGPORT/logger4life_dev"
TEST_DATABASE_URL="postgres://postgres@$PGHOST:$PGPORT/logger4life_test"

# A PostgreSQL data directory cannot be shared between platforms, and one
# worktree may be opened natively and in the dev container, so each gets its
# own cluster.
DEV_PLATFORM="$(uname -s | tr '[:upper:]' '[:lower:]')-$(uname -m)"
PGDATA="${PGDATA:-$PWD/.dev/$DEV_PLATFORM/postgres/data}"
PGSOCKETDIR="${PGSOCKETDIR:-$PWD/.dev/$DEV_PLATFORM/postgres/run}"
PGUSER="${PGUSER:-postgres}"
PGDATABASE="${PGDATABASE:-logger4life_dev}"
TEST_DATABASE="${TEST_DATABASE:-logger4life_test}"
TEST_DATABASE_COUNT="${TEST_DATABASE_COUNT:-8}"
TERN_CONFIG="${TERN_CONFIG:-postgresql/tern.conf}"
TERN_MIGRATIONS="${TERN_MIGRATIONS:-postgresql/migrations}"
PC_ADDRESS="${PC_ADDRESS:-127.0.0.1}"
PC_SERVER_LOG_FILE="${PC_SERVER_LOG_FILE:-$PWD/.dev/process-compose.log}"
PC_CLIENT_LOG_FILE="${PC_CLIENT_LOG_FILE:-$PWD/.dev/process-compose-client.log}"
PC_POSTGRES_PID_FILE="${PC_POSTGRES_PID_FILE:-$PWD/.dev/$DEV_PLATFORM/postgres/process-compose.pid}"

# Resolved here rather than at the point of use so that a machine without the
# PostgreSQL server binaries says so immediately, with installation
# instructions, instead of failing later as a process that will not start.
PGBIN="${PGBIN:-$(scripts/pgbin)}"

set +a

mkdir -p "$PGSOCKETDIR" .dev/logs

# tern.conf is deliberately not in git: it is a local file a developer may
# point elsewhere.
if [ ! -f postgresql/tern.conf ]; then
	cp postgresql/tern.example.conf postgresql/tern.conf
fi
