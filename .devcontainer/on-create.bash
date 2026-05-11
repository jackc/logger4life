#!/bin/bash
set -e

sudo chown vscode:vscode /persist/local /persist/shared
mkdir -p /persist/shared/{claude,atuin/{config,data},mise/{data,cache},psql,devcontainer-downloads,playwright}

SCRIPTDIR=$(dirname -- "$(readlink -f -- "$0")")
"$SCRIPTDIR/tern/install"
"$SCRIPTDIR/verna/install"
