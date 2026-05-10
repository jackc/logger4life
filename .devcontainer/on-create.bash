#!/bin/bash
set -e

sudo chown vscode:vscode /persist/local /persist/shared
mkdir -p /persist/shared/{claude,atuin/{config,data},mise/{data,cache},psql,devcontainer-downloads}

SCRIPTDIR=$(dirname -- "$(readlink -f -- "$0")")
"$SCRIPTDIR/fd/install"
"$SCRIPTDIR/rg/install"
"$SCRIPTDIR/tern/install"
"$SCRIPTDIR/verna/install"
"$SCRIPTDIR/watchexec/install"
